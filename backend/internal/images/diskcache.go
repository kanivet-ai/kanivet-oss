package images

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

// DefaultCacheBytes caps the on-disk layer blob cache.
const DefaultCacheBytes int64 = 2 << 30

// diskCache keeps compressed layer blobs and their file indexes under one
// directory. Blobs are evicted least-recently-used once their total passes
// the cap; indexes are small and evicted with their blob.
type diskCache struct {
	dir      string
	maxBytes int64
	mu       sync.Mutex
}

func newDiskCache(dir string, maxBytes int64) (*diskCache, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultCacheBytes
	}
	for _, sub := range []string{"blobs", "index", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	// Leftovers of interrupted downloads.
	if tmp, err := os.ReadDir(filepath.Join(dir, "tmp")); err == nil {
		for _, e := range tmp {
			_ = os.Remove(filepath.Join(dir, "tmp", e.Name()))
		}
	}
	return &diskCache{dir: dir, maxBytes: maxBytes}, nil
}

// fileKey turns "sha256:abc" into a filename that is valid on every OS.
func fileKey(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}

func (c *diskCache) blobPath(digest string) string {
	return filepath.Join(c.dir, "blobs", fileKey(digest))
}

func (c *diskCache) indexPath(digest string) string {
	return filepath.Join(c.dir, "index", fileKey(digest)+".json.gz")
}

// openBlob opens a cached compressed blob and marks it recently used.
func (c *diskCache) openBlob(digest string) (*os.File, bool) {
	p := c.blobPath(digest)
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return f, true
}

func (c *diskCache) hasIndex(digest string) bool {
	_, err := os.Stat(c.indexPath(digest))
	return err == nil
}

func (c *diskCache) loadIndex(digest string) ([]layerEntry, bool) {
	f, err := os.Open(c.indexPath(digest))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	var entries []layerEntry
	if json.NewDecoder(zr).Decode(&entries) != nil {
		return nil, false
	}
	return entries, true
}

func (c *diskCache) saveIndex(digest string, entries []layerEntry) error {
	tmp, err := os.CreateTemp(filepath.Join(c.dir, "tmp"), "index-*")
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(tmp)
	encErr := json.NewEncoder(zw).Encode(entries)
	closeErr := zw.Close()
	fileErr := tmp.Close()
	if encErr != nil || closeErr != nil || fileErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write index: %v %v %v", encErr, closeErr, fileErr)
	}
	return os.Rename(tmp.Name(), c.indexPath(digest))
}

// blobWriter streams a download to a temp file and verifies its digest
// before publishing it, so a truncated download is never served.
type blobWriter struct {
	c      *diskCache
	digest string
	f      *os.File
	h      interface {
		io.Writer
		Sum([]byte) []byte
	}
	n      int64
	limit  int64
	failed bool
}

func (c *diskCache) newBlobWriter(digest string, size int64) *blobWriter {
	// A blob bigger than half the cap would evict everything else; stream it
	// without keeping it.
	if size > c.maxBytes/2 || !strings.HasPrefix(digest, "sha256:") {
		return nil
	}
	f, err := os.CreateTemp(filepath.Join(c.dir, "tmp"), "blob-*")
	if err != nil {
		return nil
	}
	return &blobWriter{c: c, digest: digest, f: f, h: sha256.New(), limit: c.maxBytes / 2}
}

func (w *blobWriter) Write(p []byte) (int, error) {
	if w == nil || w.failed {
		return len(p), nil
	}
	w.n += int64(len(p))
	if w.n > w.limit {
		w.failed = true
		return len(p), nil
	}
	if _, err := w.f.Write(p); err != nil {
		w.failed = true
		return len(p), nil
	}
	_, _ = w.h.Write(p)
	return len(p), nil
}

// commit publishes the blob if it was read completely and matches.
func (w *blobWriter) commit() {
	if w == nil {
		return
	}
	name := w.f.Name()
	_ = w.f.Close()
	sum := "sha256:" + hex.EncodeToString(w.h.Sum(nil))
	if w.failed || sum != w.digest {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, w.c.blobPath(w.digest)); err != nil {
		_ = os.Remove(name)
		return
	}
	w.c.evict()
}

func (w *blobWriter) abort() {
	if w == nil {
		return
	}
	_ = w.f.Close()
	_ = os.Remove(w.f.Name())
}

// evict removes least-recently-used blobs (and their indexes) until the
// total is under the cap.
func (c *diskCache) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(c.dir, "blobs"))
	if err != nil {
		return
	}
	type blob struct {
		name string
		size int64
		used time.Time
	}
	var blobs []blob
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		blobs = append(blobs, blob{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= c.maxBytes {
		return
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].used.Before(blobs[j].used) })
	for _, b := range blobs {
		if total <= c.maxBytes {
			break
		}
		if os.Remove(filepath.Join(c.dir, "blobs", b.name)) == nil {
			_ = os.Remove(filepath.Join(c.dir, "index", b.name+".json.gz"))
			total -= b.size
		}
	}
}

// decompress wraps a layer stream in the decompressor its magic bytes name
// (gzip, zstd, or none for uncompressed tar).
func decompress(r io.Reader) (io.ReadCloser, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	magic, _ := br.Peek(4)
	switch {
	case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		return zr, nil
	case len(magic) == 4 && magic[0] == 0x28 && magic[1] == 0xb5 && magic[2] == 0x2f && magic[3] == 0xfd:
		zr, err := zstd.NewReader(br)
		if err != nil {
			return nil, err
		}
		return zr.IOReadCloser(), nil
	}
	return io.NopCloser(br), nil
}
