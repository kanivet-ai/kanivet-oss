package images

import (
	"archive/tar"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	whiteoutPrefix = ".wh."
	opaqueMarker   = ".wh..wh..opq"
	// maxChangesPerLayer bounds what one layer's change list returns.
	maxChangesPerLayer = 5000
)

// layerEntry is one tar header of a layer, as stored in the on-disk index.
type layerEntry struct {
	Path string `json:"p"`
	Type string `json:"t"` // file, dir, symlink, hardlink, other
	Size int64  `json:"s,omitempty"`
	Mode uint32 `json:"m,omitempty"`
	Link string `json:"l,omitempty"`
}

// cleanPath turns a tar name ("./usr/bin/", "/etc") into "usr/bin", "etc".
// The root is "".
func cleanPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

func entryType(h *tar.Header) string {
	switch h.Typeflag {
	case tar.TypeDir:
		return "dir"
	case tar.TypeSymlink:
		return "symlink"
	case tar.TypeLink:
		return "hardlink"
	case tar.TypeReg, tar.TypeRegA:
		return "file"
	}
	return "other"
}

// indexTar reads a layer tar and returns its entries without keeping data.
func indexTar(r io.Reader) ([]layerEntry, error) {
	tr := tar.NewReader(r)
	var out []layerEntry
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		p := cleanPath(h.Name)
		if p == "" {
			continue
		}
		e := layerEntry{Path: p, Type: entryType(h), Size: h.Size, Mode: uint32(h.FileInfo().Mode())}
		if h.Typeflag == tar.TypeSymlink {
			e.Link = h.Linkname
		} else if h.Typeflag == tar.TypeLink {
			e.Link = cleanPath(h.Linkname)
		}
		if e.Type == "dir" || e.Type == "symlink" {
			e.Size = 0
		}
		out = append(out, e)
	}
}

type node struct {
	name     string
	typ      string
	size     int64
	mode     uint32
	link     string
	layer    int
	children map[string]*node
	// aggregates, filled by finish()
	totalSize int64
	files     int
}

// tree is the merged filesystem of an image.
type tree struct {
	root    *node
	changes []LayerChanges
	files   int
	size    int64
}

func newDir(name string, layer int) *node {
	return &node{name: name, typ: "dir", mode: uint32(fs.ModeDir | 0o755), layer: layer, children: map[string]*node{}}
}

// mergeLayers applies layers bottom to top with OCI whiteout semantics.
func mergeLayers(layers [][]layerEntry) *tree {
	t := &tree{root: newDir("", 0), changes: make([]LayerChanges, len(layers))}
	for i, entries := range layers {
		lc := &t.changes[i]
		lc.Layer = i
		record := func(c Change) {
			switch c.Op {
			case "added":
				lc.Added++
			case "modified":
				lc.Modified++
			case "deleted":
				lc.Deleted++
			}
			c.Path = "/" + strings.TrimPrefix(c.Path, "/")
			if len(lc.Changes) < maxChangesPerLayer {
				lc.Changes = append(lc.Changes, c)
			} else {
				lc.Truncated = true
			}
		}
		for _, e := range entries {
			dir, base := path.Split(e.Path)
			dir = strings.TrimSuffix(dir, "/")
			switch {
			case base == opaqueMarker:
				d := t.ensureDir(dir, i)
				for name, c := range d.children {
					if pruneLower(c, i) {
						delete(d.children, name)
						record(Change{Path: path.Join(dir, name), Op: "deleted", Type: c.typ})
					}
				}
			case strings.HasPrefix(base, whiteoutPrefix):
				target := path.Join(dir, strings.TrimPrefix(base, whiteoutPrefix))
				if parent := t.lookup(dir, false); parent != nil && parent.children != nil {
					if c, ok := parent.children[path.Base(target)]; ok {
						delete(parent.children, path.Base(target))
						record(Change{Path: target, Op: "deleted", Type: c.typ, Size: c.size})
					}
				}
			default:
				parent := t.ensureDir(dir, i)
				existing := parent.children[base]
				op := "added"
				if existing != nil {
					op = "modified"
				}
				n := existing
				if n == nil || (n.typ == "dir") != (e.Type == "dir") {
					n = &node{name: base}
					if e.Type == "dir" {
						n.children = map[string]*node{}
					}
					parent.children[base] = n
				}
				n.typ, n.size, n.mode, n.link, n.layer = e.Type, e.Size, e.Mode, e.Link, i
				// Directories that only repeat metadata are not interesting changes.
				if !(op == "modified" && e.Type == "dir") {
					record(Change{Path: e.Path, Op: op, Type: e.Type, Size: e.Size})
				}
			}
		}
	}
	t.files, t.size = finish(t.root)
	return t
}

// pruneLower drops content from layers below `layer` under n. It reports
// whether n itself should be removed (it came from a lower layer).
func pruneLower(n *node, layer int) bool {
	if n.layer < layer {
		return true
	}
	for name, c := range n.children {
		if pruneLower(c, layer) {
			delete(n.children, name)
		}
	}
	return false
}

func (t *tree) ensureDir(p string, layer int) *node {
	n := t.root
	if p == "" {
		return n
	}
	for _, part := range strings.Split(p, "/") {
		c := n.children[part]
		if c == nil || c.children == nil {
			c = newDir(part, layer)
			n.children[part] = c
		}
		n = c
	}
	return n
}

// lookup finds a node. With follow, symlinks along the way (and at the end)
// are resolved inside the image.
func (t *tree) lookup(p string, follow bool) *node {
	n, _ := t.resolve(cleanPath(p), follow, 0)
	return n
}

func (t *tree) resolve(p string, follow bool, depth int) (*node, string) {
	if depth > 16 {
		return nil, ""
	}
	n := t.root
	if p == "" {
		return n, ""
	}
	parts := strings.Split(p, "/")
	cur := ""
	for i, part := range parts {
		if n.children == nil {
			return nil, ""
		}
		c := n.children[part]
		if c == nil {
			return nil, ""
		}
		cur = path.Join(cur, part)
		last := i == len(parts)-1
		if c.typ == "symlink" && (follow || !last) {
			target := c.link
			if !strings.HasPrefix(target, "/") {
				target = path.Join(path.Dir(cur), target)
			}
			rest := strings.Join(parts[i+1:], "/")
			return t.resolve(cleanPath(path.Join(target, rest)), follow, depth+1)
		}
		n = c
	}
	return n, cur
}

// finish computes recursive sizes and file counts.
func finish(n *node) (files int, size int64) {
	if n.children == nil {
		if n.typ == "file" {
			return 1, n.size
		}
		return 1, 0
	}
	for _, c := range n.children {
		f, s := finish(c)
		files += f
		size += s
	}
	n.files, n.totalSize = files, size
	return files, size
}

func toEntry(dir string, n *node) FileEntry {
	e := FileEntry{
		Name:       n.name,
		Path:       "/" + path.Join(dir, n.name),
		Type:       n.typ,
		Size:       n.size,
		Mode:       fs.FileMode(n.mode).String(),
		LinkTarget: n.link,
		Layer:      n.layer,
	}
	if n.children != nil {
		e.Type = "dir"
		e.Children = len(n.children)
		e.TotalSize = n.totalSize
		e.Size = 0
	}
	return e
}

// list returns a directory's entries, directories first then by name.
func (t *tree) list(p string) (*DirListing, bool) {
	clean := cleanPath(p)
	n := t.lookup(clean, true)
	if n == nil || n.children == nil {
		return nil, false
	}
	out := &DirListing{Path: "/" + clean, Entries: make([]FileEntry, 0, len(n.children)), Files: n.files, Size: n.totalSize}
	for _, c := range n.children {
		out.Entries = append(out.Entries, toEntry(clean, c))
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if (a.Type == "dir") != (b.Type == "dir") {
			return a.Type == "dir"
		}
		return a.Name < b.Name
	})
	return out, true
}

// search walks the tree for paths containing q (case-insensitive).
func (t *tree) search(q string, limit int) *SearchResult {
	res := &SearchResult{Query: q, Entries: []FileEntry{}}
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return res
	}
	var walk func(dir string, n *node) bool
	walk = func(dir string, n *node) bool {
		names := make([]string, 0, len(n.children))
		for name := range n.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := n.children[name]
			full := path.Join(dir, name)
			if strings.Contains(strings.ToLower(full), needle) {
				if len(res.Entries) >= limit {
					res.Truncated = true
					return false
				}
				res.Entries = append(res.Entries, toEntry(dir, c))
			}
			if c.children != nil && !walk(full, c) {
				return false
			}
		}
		return true
	}
	walk("", t.root)
	return res
}
