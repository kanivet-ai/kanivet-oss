package storage

import (
	"fmt"
	"testing"
)

func searchFound(t *testing.T, idx *MemoryIndex, query, name string) bool {
	t.Helper()
	results, err := idx.Search(SearchQuery{Text: query, Limit: 50})
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	for _, r := range results {
		if r.Resource.Name == name {
			return true
		}
	}
	return false
}

func TestSearchMultiTokenQueryMatchesTokenizedName(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("c1", "", "v1", "Pod", "kube-system", "coredns-5d78c9869d-xk2pq")); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !searchFound(t, idx, "coredns 5d78", "coredns-5d78c9869d-xk2pq") {
		t.Fatal("multi-token query did not match tokenized name")
	}
}

func TestSearchPartialHyphenatedNameMatches(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("c1", "", "v1", "Pod", "kube-system", "coredns-5d78c9869d-xk2pq")); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !searchFound(t, idx, "coredns-5d78", "coredns-5d78c9869d-xk2pq") {
		t.Fatal("partial hyphenated query did not match")
	}
}

func TestSearchMidSubstringFallbackStillMatches(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("c1", "", "v1", "Pod", "kube-system", "coredns-5d78c9869d-xk2pq")); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !searchFound(t, idx, "9869d", "coredns-5d78c9869d-xk2pq") {
		t.Fatal("mid-substring query did not match via fallback")
	}
}

func TestSearchTypoKindMatchesViaBKTree(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("c1", "apps", "v1", "Deployment", "default", "web-server")); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !searchFound(t, idx, "deploymnet", "web-server") {
		t.Fatal("typo kind query did not match via BK-tree")
	}
}

func TestLevenshteinBounded(t *testing.T) {
	cases := []struct {
		s1, s2 string
		maxD   int
		want   int
	}{
		{"kitten", "sitting", 3, 3},
		{"kitten", "sitting", 2, 3},
		{"coredns", "coredns", 1, 0},
		{"abc", "abcdefgh", 2, 3},
		{"deployment", "deploymnet", 3, 2},
		{"", "abc", 3, 3},
		{"abc", "", 2, 3},
		{"nginx", "postgresql", 3, 4},
	}
	for _, c := range cases {
		got := levenshteinBounded(c.s1, c.s2, c.maxD)
		exact := levenshteinDistance(c.s1, c.s2)
		if exact <= c.maxD {
			if got != exact {
				t.Errorf("levenshteinBounded(%q,%q,%d)=%d, want exact %d", c.s1, c.s2, c.maxD, got, exact)
			}
		} else if got <= c.maxD {
			t.Errorf("levenshteinBounded(%q,%q,%d)=%d, want > maxD", c.s1, c.s2, c.maxD, got)
		}
		if got != c.want && c.want <= c.maxD {
			t.Errorf("levenshteinBounded(%q,%q,%d)=%d, want %d", c.s1, c.s2, c.maxD, got, c.want)
		}
	}
}

func TestFuzzySimilarityThreshold(t *testing.T) {
	if s := fuzzySimilarity("deployment", "deploymnet"); s <= 0.6 {
		t.Errorf("close strings similarity %v, want > 0.6", s)
	}
	if s := fuzzySimilarity("nginx", "postgresql"); s != 0 {
		t.Errorf("distant strings similarity %v, want 0", s)
	}
	if s := fuzzySimilarity("pod", "pod"); s != 1.0 {
		t.Errorf("equal strings similarity %v, want 1.0", s)
	}
}

func BenchmarkSearchPartialHyphenatedName(b *testing.B) {
	idx := NewMemoryIndex()
	for i := 0; i < 20000; i++ {
		r := testResource("c1", "apps", "v1", "Pod", "ns", fmt.Sprintf("app-%d-7f8d9c%d-x%dq", i, i, i))
		if err := idx.Index(r); err != nil {
			b.Fatal(err)
		}
	}
	if err := idx.Index(testResource("c1", "", "v1", "Pod", "kube-system", "coredns-5d78c9869d-xk2pq")); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := idx.Search(SearchQuery{Text: "coredns-5d78", Limit: 50}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchMidSubstringFallback(b *testing.B) {
	idx := NewMemoryIndex()
	for i := 0; i < 20000; i++ {
		r := testResource("c1", "apps", "v1", "Pod", "ns", fmt.Sprintf("app-%d-7f8d9c%d-x%dq", i, i, i))
		if err := idx.Index(r); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := idx.Search(SearchQuery{Text: "9869d", Limit: 50}); err != nil {
			b.Fatal(err)
		}
	}
}
