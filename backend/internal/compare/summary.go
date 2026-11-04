package compare

import (
	"fmt"
	"sort"
	"strings"
)

// Summarize explains the changes in terms a person reads first: the image
// differs, replicas 2 vs 5, env FOO only in A. Every change is covered by at
// most one insight; changes no rule recognises are grouped by section.
func Summarize(a, b map[string]any, changes []FieldChange) []Insight {
	s := &summarizer{changes: changes, used: make([]bool, len(changes))}

	ka, _ := a["kind"].(string)
	kb, _ := b["kind"].(string)
	if ka != kb {
		s.add(Insight{Category: "kind", Title: "Different kinds", A: ka, B: kb}, "kind", "apiVersion")
	} else {
		s.add(Insight{Category: "kind", Title: "API version differs", A: str(a["apiVersion"]), B: str(b["apiVersion"])}, "apiVersion")
	}
	s.identity(a, b)

	if ka == kb {
		if p := PodSpecPath(ka); p != nil {
			s.replicas(a, b)
			s.podSpec(strings.Join(p, "."), mapAt(a, p...), mapAt(b, p...))
		}
		switch ka {
		case "ConfigMap", "Secret":
			s.dataKeys(a, b, "data")
			s.dataKeys(a, b, "binaryData")
		case "Service":
			s.prefix("ports", "Service ports differ", "spec.ports")
			s.prefix("scheduling", "Selector differs", "spec.selector")
			s.scalar("spec", "Service type differs", "spec.type")
		}
	}
	s.mapKeys(a, b, "labels", "metadata.labels")
	s.mapKeys(a, b, "annotations", "metadata.annotations")
	s.prefix("status", "Status differs", "status")

	// Whatever is left, grouped by its top two path segments.
	groups := map[string][]int{}
	var order []string
	for i, c := range s.changes {
		if s.used[i] {
			continue
		}
		g := section(c.Path)
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], i)
	}
	for _, g := range order {
		idx := groups[g]
		in := Insight{Category: "spec", Paths: nil}
		if strings.HasPrefix(g, "metadata") {
			in.Category = "other"
		}
		if len(idx) == 1 {
			c := s.changes[idx[0]]
			in.Title = fmt.Sprintf("%s differs", c.Path)
			in.A, in.B = c.A, c.B
		} else {
			in.Title = fmt.Sprintf("%s: %d fields differ", g, len(idx))
		}
		for _, i := range idx {
			in.Paths = append(in.Paths, s.changes[i].Path)
			s.used[i] = true
		}
		s.out = append(s.out, in)
	}
	return s.out
}

type summarizer struct {
	changes []FieldChange
	used    []bool
	out     []Insight
}

func section(path string) string {
	parts := splitPath(path)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, ".")
}

// splitPath splits on dots outside brackets.
func splitPath(p string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range p {
		switch r {
		case '[':
			if depth == 0 && i > start {
				parts = append(parts, p[start:i])
				start = i
			}
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				if i > start {
					parts = append(parts, p[start:i])
				}
				start = i + 1
			}
		}
	}
	if start < len(p) {
		parts = append(parts, p[start:])
	}
	return parts
}

func hasPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+".") || strings.HasPrefix(path, prefix+"[")
}

// claim marks unused changes under any prefix as used and returns their paths.
func (s *summarizer) claim(prefixes ...string) []string {
	var paths []string
	for i, c := range s.changes {
		if s.used[i] {
			continue
		}
		for _, p := range prefixes {
			if hasPrefix(c.Path, p) {
				s.used[i] = true
				paths = append(paths, c.Path)
				break
			}
		}
	}
	return paths
}

// add records in when any change under prefixes is still unexplained.
func (s *summarizer) add(in Insight, prefixes ...string) {
	if paths := s.claim(prefixes...); len(paths) > 0 {
		in.Paths = paths
		s.out = append(s.out, in)
	}
}

func (s *summarizer) prefix(category, title, prefix string) {
	s.add(Insight{Category: category, Title: title}, prefix)
}

func (s *summarizer) scalar(category, title, path string) {
	for _, c := range s.changes {
		if c.Path == path {
			s.add(Insight{Category: category, Title: title, A: orNone(c.A), B: orNone(c.B)}, path)
			return
		}
	}
}

func (s *summarizer) identity(a, b map[string]any) {
	na, nb := str(mapAt(a, "metadata")["name"]), str(mapAt(b, "metadata")["name"])
	sa, sb := str(mapAt(a, "metadata")["namespace"]), str(mapAt(b, "metadata")["namespace"])
	if na != nb {
		s.add(Insight{Category: "identity", Title: "Names differ", A: na, B: nb}, "metadata.name")
	}
	if sa != sb {
		s.add(Insight{Category: "identity", Title: "Namespaces differ", A: orNone(sa), B: orNone(sb)}, "metadata.namespace")
	}
}

func (s *summarizer) replicas(a, b map[string]any) {
	ra, rb := mapAt(a, "spec")["replicas"], mapAt(b, "spec")["replicas"]
	if ra == nil && rb == nil {
		return
	}
	s.add(Insight{Category: "replicas", Title: "Replicas differ", A: orNone(render0(ra)), B: orNone(render0(rb))}, "spec.replicas")
}

func (s *summarizer) podSpec(base string, pa, pb map[string]any) {
	for _, list := range []string{"initContainers", "containers"} {
		ca, cb := containersByName(pa, list), containersByName(pb, list)
		label := "Container"
		if list == "initContainers" {
			label = "Init container"
		}
		for _, name := range unionNames(ca.order, cb.order) {
			p := fmt.Sprintf("%s.%s[%s]", base, list, name)
			x, y := ca.byName[name], cb.byName[name]
			switch {
			case x == nil:
				s.add(Insight{Category: "spec", Title: fmt.Sprintf("%s %s only in B", label, name)}, p)
				continue
			case y == nil:
				s.add(Insight{Category: "spec", Title: fmt.Sprintf("%s %s only in A", label, name)}, p)
				continue
			}
			s.add(Insight{Category: "image", Title: fmt.Sprintf("Image differs (%s)", name), A: orNone(str(x["image"])), B: orNone(str(y["image"]))}, p+".image")
			s.env(p, name, x, y)
			s.add(Insight{Category: "resources", Title: fmt.Sprintf("Resources differ (%s)", name), A: resourcesLine(x), B: resourcesLine(y)}, p+".resources")
			s.add(Insight{Category: "ports", Title: fmt.Sprintf("Ports differ (%s)", name)}, p+".ports")
			for _, probe := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
				s.add(Insight{Category: "probes", Title: fmt.Sprintf("%s differs (%s)", probeLabel(probe), name)}, p+"."+probe)
			}
			s.add(Insight{Category: "spec", Title: fmt.Sprintf("Command or args differ (%s)", name)}, p+".command", p+".args")
			s.add(Insight{Category: "volumes", Title: fmt.Sprintf("Volume mounts differ (%s)", name)}, p+".volumeMounts")
			s.add(Insight{Category: "spec", Title: fmt.Sprintf("Container %s differs", name)}, p)
		}
	}
	s.add(Insight{Category: "volumes", Title: "Volumes differ"}, base+".volumes")
	s.add(Insight{Category: "scheduling", Title: "Scheduling differs (node selector, affinity, tolerations)"},
		base+".nodeSelector", base+".affinity", base+".tolerations", base+".topologySpreadConstraints", base+".priorityClassName")
	s.scalar("spec", "Service account differs", base+".serviceAccountName")
}

func probeLabel(p string) string {
	switch p {
	case "livenessProbe":
		return "Liveness probe"
	case "readinessProbe":
		return "Readiness probe"
	}
	return "Startup probe"
}

func (s *summarizer) env(base, container string, x, y map[string]any) {
	ea, eb := envMap(x), envMap(y)
	var onlyA, onlyB, diff []string
	for k, v := range ea {
		w, ok := eb[k]
		if !ok {
			onlyA = append(onlyA, k)
		} else if v != w {
			diff = append(diff, k)
		}
	}
	for k := range eb {
		if _, ok := ea[k]; !ok {
			onlyB = append(onlyB, k)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	sort.Strings(diff)
	if len(onlyA)+len(onlyB)+len(diff) == 0 {
		s.add(Insight{Category: "env", Title: fmt.Sprintf("Env sources differ (%s)", container)}, base+".envFrom", base+".env")
		return
	}
	paths := s.claim(base+".env", base+".envFrom")
	if len(onlyA) > 0 {
		s.out = append(s.out, Insight{Category: "env", Title: fmt.Sprintf("Env %s only in A (%s)", strings.Join(onlyA, ", "), container), Paths: paths})
	}
	if len(onlyB) > 0 {
		s.out = append(s.out, Insight{Category: "env", Title: fmt.Sprintf("Env %s only in B (%s)", strings.Join(onlyB, ", "), container), Paths: paths})
	}
	for _, k := range diff {
		s.out = append(s.out, Insight{Category: "env", Title: fmt.Sprintf("Env %s differs (%s)", k, container), A: ea[k], B: eb[k], Paths: paths})
	}
}

func envMap(c map[string]any) map[string]string {
	out := map[string]string{}
	list, _ := c["env"].([]any)
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name := str(m["name"])
		if v, ok := m["value"]; ok {
			out[name] = str(v)
		} else if vf, ok := m["valueFrom"]; ok {
			out[name] = "from " + render(vf)
		} else {
			out[name] = ""
		}
	}
	return out
}

func resourcesLine(c map[string]any) string {
	r, _ := c["resources"].(map[string]any)
	if len(r) == 0 {
		return "none"
	}
	var parts []string
	for _, section := range []string{"requests", "limits"} {
		m, _ := r[section].(map[string]any)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s.%s=%s", section, k, str(m[k])))
		}
	}
	return strings.Join(parts, " ")
}

func (s *summarizer) dataKeys(a, b map[string]any, field string) {
	da, _ := a[field].(map[string]any)
	db, _ := b[field].(map[string]any)
	if da == nil && db == nil {
		return
	}
	var onlyA, onlyB, diff []string
	for k, v := range da {
		w, ok := db[k]
		if !ok {
			onlyA = append(onlyA, k)
		} else if !looseEqual(v, w) {
			diff = append(diff, k)
		}
	}
	for k := range db {
		if _, ok := da[k]; !ok {
			onlyB = append(onlyB, k)
		}
	}
	if len(onlyA)+len(onlyB)+len(diff) == 0 {
		return
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	sort.Strings(diff)
	paths := s.claim(field)
	if len(onlyA) > 0 {
		s.out = append(s.out, Insight{Category: "data", Title: fmt.Sprintf("Keys only in A: %s", strings.Join(onlyA, ", ")), Paths: paths})
	}
	if len(onlyB) > 0 {
		s.out = append(s.out, Insight{Category: "data", Title: fmt.Sprintf("Keys only in B: %s", strings.Join(onlyB, ", ")), Paths: paths})
	}
	if len(diff) > 0 {
		s.out = append(s.out, Insight{Category: "data", Title: fmt.Sprintf("Values differ: %s", strings.Join(diff, ", ")), Paths: paths})
	}
}

func (s *summarizer) mapKeys(a, b map[string]any, what, prefix string) {
	ma, _ := mapAt(a, "metadata")[what].(map[string]any)
	mb, _ := mapAt(b, "metadata")[what].(map[string]any)
	n := 0
	for k, v := range ma {
		if w, ok := mb[k]; !ok || !looseEqual(v, w) {
			n++
		}
	}
	for k := range mb {
		if _, ok := ma[k]; !ok {
			n++
		}
	}
	if n == 0 {
		return
	}
	title := fmt.Sprintf("%d %s differ", n, what)
	if n == 1 {
		title = fmt.Sprintf("1 %s differs", strings.TrimSuffix(what, "s"))
	}
	s.add(Insight{Category: what, Title: title}, prefix)
}

type named struct {
	order  []string
	byName map[string]map[string]any
}

func containersByName(ps map[string]any, list string) named {
	n := named{byName: map[string]map[string]any{}}
	cs, _ := ps[list].([]any)
	for _, c := range cs {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		name := str(m["name"])
		n.order = append(n.order, name)
		n.byName[name] = m
	}
	return n
}

func unionNames(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range [][]string{a, b} {
		for _, n := range l {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return render(v)
}

func render0(v any) string {
	if v == nil {
		return ""
	}
	return render(v)
}

func orNone(s string) string {
	if s == "" {
		return "not set"
	}
	return s
}
