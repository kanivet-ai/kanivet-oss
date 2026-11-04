package changes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// maxFieldsPerEntry caps the paths one managedFields entry lists.
const maxFieldsPerEntry = 24

// managedPaths turns a fieldsV1 tree into leaf paths: f:spec/f:replicas
// becomes spec.replicas, k:{"name":"web"} becomes [web].
func managedPaths(raw []byte) []string {
	var tree map[string]interface{}
	if len(raw) == 0 || json.Unmarshal(raw, &tree) != nil {
		return nil
	}
	var out []string
	var walk func(prefix string, node map[string]interface{})
	walk = func(prefix string, node map[string]interface{}) {
		leaf := true
		for k, v := range node {
			if k == "." {
				continue
			}
			leaf = false
			seg := managedSegment(k)
			p := prefix
			switch {
			case strings.HasPrefix(seg, "["):
				p += seg
			case p == "":
				p = seg
			default:
				p += "." + seg
			}
			child, _ := v.(map[string]interface{})
			walk(p, child)
		}
		if leaf && prefix != "" {
			out = append(out, prefix)
		}
	}
	walk("", tree)
	sort.Strings(out)
	return out
}

func managedSegment(k string) string {
	switch {
	case strings.HasPrefix(k, "f:"):
		// Label and annotation keys hold dots: keep them in one segment.
		if strings.ContainsAny(k[2:], "./") {
			return "[" + k[2:] + "]"
		}
		return k[2:]
	case strings.HasPrefix(k, "k:"):
		var key map[string]interface{}
		if json.Unmarshal([]byte(k[2:]), &key) == nil {
			if n, ok := key["name"]; ok {
				return fmt.Sprintf("[%v]", n)
			}
			if n, ok := key["containerPort"]; ok {
				return fmt.Sprintf("[%v]", n)
			}
		}
		return "[" + k[2:] + "]"
	case strings.HasPrefix(k, "v:"):
		return "[" + strings.Trim(k[2:], `"`) + "]"
	case strings.HasPrefix(k, "i:"):
		return "[" + k[2:] + "]"
	}
	return k
}

// condense keeps paths to a readable depth and drops duplicates.
func condense(paths []string, depth int) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		segs := splitPath(p)
		if len(segs) > depth {
			segs = segs[:depth]
		}
		c := joinPath(segs)
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// splitPath splits a.b[c].d into a, b[c], d.
func splitPath(p string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range p {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				if i > start {
					out = append(out, p[start:i])
				}
				start = i + 1
			}
		}
	}
	if start < len(p) {
		out = append(out, p[start:])
	}
	return out
}

func joinPath(segs []string) string {
	return strings.Join(segs, ".")
}

// managedFieldsEntries turns an object's managedFields into "edited by" entries.
// Status-subresource writes are controller heartbeats and are skipped, as are
// metadata-only writes by controllers that only touch finalizers/ownerRefs.
func managedFieldsEntries(subject ResourceRef, mf []metav1.ManagedFieldsEntry) []Entry {
	var out []Entry
	for i, m := range mf {
		if m.Subresource == "status" || m.Time == nil {
			continue
		}
		var raw []byte
		if m.FieldsV1 != nil {
			raw = m.FieldsV1.Raw
		}
		paths := managedPaths(raw)
		if len(paths) == 0 {
			continue
		}
		e := Entry{
			ID:      fmt.Sprintf("mf:%s:%d", m.Manager, i),
			Source:  SourceManagedFields,
			Time:    m.Time.Time,
			Subject: subject,
			Actor:   m.Manager,
		}
		if m.Subresource != "" {
			e.Actor += " (" + m.Subresource + ")"
		}
		cats, summary := classifyManagedPaths(subject.Kind, paths)
		e.Categories = cats
		e.Fields = condense(paths, 4)
		if len(e.Fields) > maxFieldsPerEntry {
			e.Fields = append(e.Fields[:maxFieldsPerEntry], fmt.Sprintf("… %d more", len(e.Fields)-maxFieldsPerEntry))
		}
		verb := "Edited"
		if m.Operation == metav1.ManagedFieldsOperationApply {
			verb = "Applied"
		}
		e.Title = fmt.Sprintf("%s by %s", verb, e.Actor)
		e.Summary = summary
		out = append(out, e)
	}
	return out
}

func classifyManagedPaths(kind string, paths []string) ([]Category, string) {
	cats := map[Category]bool{}
	var secretKeys []string
	for _, p := range paths {
		switch {
		case kind == "Secret" && (strings.HasPrefix(p, "data.") || strings.HasPrefix(p, "stringData.")):
			cats[CatSecret] = true
			secretKeys = append(secretKeys, p[strings.Index(p, ".")+1:])
		case kind == "ConfigMap" && (strings.HasPrefix(p, "data.") || strings.HasPrefix(p, "binaryData.")):
			cats[CatConfig] = true
		case p == "spec.replicas":
			cats[CatReplicas] = true
		case strings.HasSuffix(p, ".image"):
			cats[CatImage] = true
		case strings.Contains(p, ".env["):
			cats[CatEnv] = true
		case strings.Contains(p, ".resources."):
			cats[CatResources] = true
		case strings.Contains(p, "kubectl.kubernetes.io/restartedAt"):
			cats[CatRestart] = true
		}
	}
	var out []Category
	for _, c := range []Category{CatSecret, CatConfig, CatReplicas, CatImage, CatEnv, CatResources, CatRestart} {
		if cats[c] {
			out = append(out, c)
		}
	}
	out = append(out, CatEdit)
	summary := ""
	if len(secretKeys) > 0 {
		sort.Strings(secretKeys)
		summary = fmt.Sprintf("owns keys [%s]", strings.Join(secretKeys, ", "))
	}
	return out, summary
}
