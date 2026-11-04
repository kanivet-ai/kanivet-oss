package changes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// noisyTemplatePaths are pod-template fields that differ between revisions
// without the user having changed anything.
var noisyTemplatePaths = []string{
	"metadata.labels.pod-template-hash",
	"metadata.creationTimestamp",
	"metadata.annotations.deployment.kubernetes.io/revision",
	"metadata.annotations.kubectl.kubernetes.io/last-applied-configuration",
}

// flatten turns a JSON-like value into path → scalar. List items that carry
// a "name" are keyed by it (containers[web]) so reordering or inserting one
// does not show every later item as changed.
func flatten(prefix string, v interface{}, out map[string]string) {
	switch t := v.(type) {
	case map[string]interface{}:
		if len(t) == 0 && prefix != "" {
			out[prefix] = "{}"
		}
		for k, val := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flatten(p, val, out)
		}
	case []interface{}:
		if len(t) == 0 {
			out[prefix] = "[]"
		}
		for i, item := range t {
			key := fmt.Sprint(i)
			if m, ok := item.(map[string]interface{}); ok {
				if n, ok := m["name"].(string); ok && n != "" {
					key = n
				}
			}
			flatten(prefix+"["+key+"]", item, out)
		}
	case nil:
		out[prefix] = "null"
	case string:
		out[prefix] = t
	default:
		b, _ := json.Marshal(t)
		out[prefix] = string(b)
	}
}

func isNoisy(path string) bool {
	for _, n := range noisyTemplatePaths {
		if path == n || strings.HasPrefix(path, n+".") {
			return true
		}
	}
	return false
}

// diffValues lists the scalar fields that differ between a and b, sorted by
// path. Either side may be nil (everything added or removed).
func diffValues(a, b interface{}) []FieldChange {
	fa, fb := map[string]string{}, map[string]string{}
	if a != nil {
		flatten("", a, fa)
	}
	if b != nil {
		flatten("", b, fb)
	}
	var out []FieldChange
	for p, ov := range fa {
		if isNoisy(p) {
			continue
		}
		nv, ok := fb[p]
		if !ok {
			out = append(out, FieldChange{Path: p, Old: ov})
		} else if nv != ov {
			out = append(out, FieldChange{Path: p, Old: ov, New: nv})
		}
	}
	for p, nv := range fb {
		if isNoisy(p) {
			continue
		}
		if _, ok := fa[p]; !ok {
			out = append(out, FieldChange{Path: p, New: nv})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// containerOf returns the container a template path belongs to, or "".
func containerOf(path string) string {
	for _, seg := range []string{"spec.containers[", "spec.initContainers["} {
		if i := strings.Index(path, seg); i >= 0 {
			rest := path[i+len(seg):]
			if j := strings.Index(rest, "]"); j >= 0 {
				return rest[:j]
			}
		}
	}
	return ""
}

// classifyTemplateDiff names what a pod-template diff changed and summarises
// it in one line ("image web nginx:1.25 → nginx:1.26").
func classifyTemplateDiff(diff []FieldChange) ([]Category, string) {
	cats := map[Category]bool{}
	var parts []string
	envTouched := map[string]bool{}
	for _, d := range diff {
		p := d.Path
		switch {
		case strings.HasSuffix(p, ".image"):
			cats[CatImage] = true
			c := containerOf(p)
			if d.Old != "" && d.New != "" {
				parts = append(parts, fmt.Sprintf("image %s %s → %s", c, shortImage(d.Old), shortImage(d.New)))
			} else if d.New != "" {
				parts = append(parts, fmt.Sprintf("container %s added (%s)", c, shortImage(d.New)))
			} else {
				parts = append(parts, fmt.Sprintf("container %s removed", c))
			}
		case strings.Contains(p, ".env[") || strings.Contains(p, ".envFrom["):
			cats[CatEnv] = true
			if i := strings.Index(p, ".env["); i >= 0 {
				rest := p[i+5:]
				if j := strings.Index(rest, "]"); j >= 0 {
					envTouched[rest[:j]] = true
				}
			}
		case strings.Contains(p, ".resources."):
			cats[CatResources] = true
		case strings.HasPrefix(p, "spec.volumes[") || strings.Contains(p, ".volumeMounts["):
			cats[CatConfig] = true
		case strings.HasPrefix(p, "metadata.annotations.kubectl.kubernetes.io/restartedAt"):
			cats[CatRestart] = true
			parts = append(parts, "rollout restart")
		default:
			cats[CatOther] = true
		}
	}
	if len(envTouched) > 0 {
		parts = append(parts, "env "+strings.Join(sortedKeys(envTouched), ", "))
	}
	if cats[CatResources] {
		parts = append(parts, "resources changed")
	}
	if cats[CatConfig] {
		parts = append(parts, "volumes changed")
	}
	out := []Category{CatRollout}
	for _, c := range []Category{CatImage, CatEnv, CatResources, CatConfig, CatRestart, CatOther} {
		if cats[c] {
			out = append(out, c)
		}
	}
	if len(parts) == 0 && cats[CatOther] {
		parts = append(parts, fmt.Sprintf("%d field(s) changed", len(diff)))
	}
	return out, strings.Join(parts, "; ")
}

func shortImage(img string) string {
	if i := strings.LastIndex(img, "/"); i >= 0 {
		return img[i+1:]
	}
	return img
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toYAML(v interface{}) string {
	if v == nil {
		return ""
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// toMap round-trips a typed object through JSON into a plain map.
func toMap(v interface{}) map[string]interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	return m
}

// redactSecretValues replaces every value under data/stringData with a
// placeholder so no Secret payload ever leaves this package.
func redactSecretValues(obj map[string]interface{}) {
	for _, k := range []string{"data", "stringData"} {
		if m, ok := obj[k].(map[string]interface{}); ok {
			for key := range m {
				m[key] = "<redacted>"
			}
		}
	}
}
