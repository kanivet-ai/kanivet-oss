package compare

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

const maxValueLen = 240

// Diff lists the differences between two normalized objects, in document order.
func Diff(a, b any) []FieldChange {
	var out []FieldChange
	diffAt("", a, b, &out)
	return out
}

func joinKey(path, key string) string {
	if isIdentKey(key) {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	return path + "[" + strconv.Quote(key) + "]"
}

func diffAt(path string, a, b any, out *[]FieldChange) {
	if a == nil && b == nil {
		return
	}
	if a == nil {
		*out = append(*out, FieldChange{Path: path, Type: OnlyInB, B: render(b)})
		return
	}
	if b == nil {
		*out = append(*out, FieldChange{Path: path, Type: OnlyInA, A: render(a)})
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			break
		}
		for _, k := range orderedKeys(av, bv) {
			diffAt(joinKey(path, k), av[k], bv[k], out)
		}
		return
	case []any:
		bv, ok := b.([]any)
		if !ok {
			break
		}
		an, aok := namedItems(av)
		bn, bok := namedItems(bv)
		if aok && bok {
			seen := map[string]bool{}
			names := []string{}
			for _, it := range av {
				n := itemName(it)
				seen[n] = true
				names = append(names, n)
			}
			for _, it := range bv {
				if n := itemName(it); !seen[n] {
					names = append(names, n)
				}
			}
			for _, n := range names {
				diffAt(path+"["+n+"]", an[n], bn[n], out)
			}
			return
		}
		max := len(av)
		if len(bv) > max {
			max = len(bv)
		}
		for i := 0; i < max; i++ {
			var x, y any
			if i < len(av) {
				x = av[i]
			}
			if i < len(bv) {
				y = bv[i]
			}
			diffAt(path+"["+strconv.Itoa(i)+"]", x, y, out)
		}
		return
	}
	if !looseEqual(a, b) {
		*out = append(*out, FieldChange{Path: path, Type: Changed, A: render(a), B: render(b)})
	}
}

// orderedKeys puts apiVersion/kind/metadata first and status last, the rest sorted.
func orderedKeys(a, b map[string]any) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := keyRank(keys[i]), keyRank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func keyRank(k string) int {
	switch k {
	case "apiVersion":
		return 0
	case "kind":
		return 1
	case "metadata":
		return 2
	case "name":
		return 3
	case "status":
		return 9
	}
	return 5
}

func render(v any) string {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err != nil {
			s = fmt.Sprint(x)
		} else {
			s = string(b)
		}
	default:
		s = fmt.Sprint(x)
	}
	if len(s) > maxValueLen {
		s = s[:maxValueLen] + "…"
	}
	return s
}
