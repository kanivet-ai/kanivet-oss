package watcher

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func names(items []map[string]interface{}) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item["name"].(string)
	}
	return out
}

func TestSortCachedItemsOrders(t *testing.T) {
	item := func(name string, kv ...interface{}) map[string]interface{} {
		m := map[string]interface{}{"name": name, "namespace": "ns"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		by, order string
		items     []map[string]interface{}
		want      []string
	}{
		{"name", "asc", []map[string]interface{}{item("b"), item("C"), item("a")}, []string{"a", "b", "C"}},
		{"name", "desc", []map[string]interface{}{item("b"), item("C"), item("a")}, []string{"C", "b", "a"}},
		{"age", "desc", []map[string]interface{}{
			item("old", "creationTimestamp", "2026-01-01T00:00:00Z"),
			item("new", "creationTimestamp", "2026-03-01T00:00:00Z"),
			item("none"),
		}, []string{"new", "old", "none"}},
		{"age", "asc", []map[string]interface{}{
			item("new", "creationTimestamp", "2026-03-01T00:00:00Z"),
			item("none"),
			item("old", "creationTimestamp", "2026-01-01T00:00:00Z"),
		}, []string{"none", "old", "new"}},
		{"restarts", "asc", []map[string]interface{}{
			item("ten", "restarts", 10), item("two", "restarts", int64(2)), item("half", "restarts", 0.5),
		}, []string{"half", "two", "ten"}},
		// Ties fall back to namespace and name whatever the direction.
		{"age", "desc", []map[string]interface{}{
			item("b", "creationTimestamp", "2026-01-01T00:00:00Z"),
			item("a", "creationTimestamp", "2026-01-01T00:00:00Z"),
			item("c", "creationTimestamp", "2026-01-01T00:00:00Z"),
		}, []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		sortCachedItems(tc.items, tc.by, tc.order)
		if got := names(tc.items); !slices.Equal(got, tc.want) {
			t.Errorf("sort by %s %s = %v, want %v", tc.by, tc.order, got, tc.want)
		}
	}
}

// legacyCompare is the comparator sortCachedItems used before keys were
// precomputed; precomputing them must not change any order it defined.
func legacyCompare(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch va := a.(type) {
	case string:
		if vb, ok := b.(string); ok {
			return strings.Compare(strings.ToLower(va), strings.ToLower(vb))
		}
	case int:
		if vb, ok := b.(int); ok {
			return compareOrdered(va, vb)
		}
	case int64:
		if vb, ok := b.(int64); ok {
			return compareOrdered(va, vb)
		}
	case float64:
		if vb, ok := b.(float64); ok {
			return compareOrdered(va, vb)
		}
	}
	sa, _ := a.(string)
	sb, _ := b.(string)
	return strings.Compare(strings.ToLower(sa), strings.ToLower(sb))
}

func compareOrdered[T int | int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func randomRows(r *rand.Rand, n int) []map[string]interface{} {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]map[string]interface{}, n)
	for i := range rows {
		row := map[string]interface{}{
			// Unique values, so the only order is the comparator's.
			"name":              fmt.Sprintf("%c-app-%06d", 'A'+r.IntN(26)+32*r.IntN(2), i),
			"namespace":         "ns",
			"creationTimestamp": start.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"restarts":          int64(i),
		}
		if r.IntN(10) == 0 {
			delete(row, "creationTimestamp")
			delete(row, "restarts")
			row["name"] = fmt.Sprintf("~nil-%06d", i)
		}
		rows[i] = row
	}
	r.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	return rows
}

func TestSortCachedItemsKeepsLegacyOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, by := range []string{"name", "age", "restarts"} {
		for _, order := range []string{"asc", "desc"} {
			rows := randomRows(r, 3000)
			want := slices.Clone(rows)
			field := by
			if by == "age" {
				field = "creationTimestamp"
			}
			sort.SliceStable(want, func(i, j int) bool {
				c := legacyCompare(want[i][field], want[j][field])
				if order == "desc" {
					return c > 0
				}
				return c < 0
			})
			sortCachedItems(rows, by, order)
			got, exp := names(rows), names(want)
			// Rows without the field tie with each other, and only their
			// relative order may differ.
			for i := range got {
				if strings.HasPrefix(exp[i], "~nil-") && strings.HasPrefix(got[i], "~nil-") {
					continue
				}
				if got[i] != exp[i] {
					t.Fatalf("sort by %s %s differs at %d: %s, legacy %s", by, order, i, got[i], exp[i])
				}
			}
		}
	}
}

func BenchmarkSortCachedItems(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{5000, 50000} {
		rows := randomRows(r, n)
		for _, by := range []string{"age", "name"} {
			b.Run(fmt.Sprintf("%d/%s", n, by), func(b *testing.B) {
				b.ReportAllocs()
				buf := make([]map[string]interface{}, len(rows))
				for range b.N {
					copy(buf, rows)
					sortCachedItems(buf, by, "desc")
				}
			})
		}
	}
}
