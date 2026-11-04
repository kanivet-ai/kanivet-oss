package images

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"unicode/utf8"
)

// readTarEntry scans a tar stream for path p and returns up to limit bytes.
func readTarEntry(ctx context.Context, r io.Reader, p string, limit int64) ([]byte, bool, error) {
	tr := tar.NewReader(r)
	want := cleanPath(p)
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if cleanPath(h.Name) != want || (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, limit))
		return data, true, err
	}
}

// looksBinary uses git's heuristic: a NUL byte in the first 8 KiB, or text
// that is not UTF-8.
func looksBinary(b []byte) bool {
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	return !utf8.Valid(trimPartialRune(head))
}

// trimPartialRune drops an incomplete UTF-8 sequence cut off by a limit.
func trimPartialRune(b []byte) []byte {
	for back := 1; back <= utf8.UTFMax && back <= len(b); back++ {
		start := len(b) - back
		if utf8.RuneStart(b[start]) {
			if !utf8.FullRune(b[start:]) {
				return b[:start]
			}
			return b
		}
	}
	return b
}
