package storage

import (
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/kanivet/backend/internal/utils"
)

// buildTokensReference is the pre-pooling inline tokenization that Index() used.
// buildTokens must produce byte-identical float32 results to this.
func buildTokensReference(r SearchableResource) map[string]float32 {
	tokens := make(map[string]float32)
	for _, t := range tokenize(r.Name) {
		tokens[t] += 3.0
	}
	for _, t := range prefixTokens(r.Name) {
		tokens[t] += 3.0
	}
	for _, t := range tokenize(r.Namespace) {
		tokens[t] += 2.0
	}
	for _, t := range tokenize(r.Kind) {
		tokens[t] += 3.4
	}
	for _, t := range prefixTokens(r.Kind) {
		tokens[t] += 3.4
	}
	if k := strings.ToLower(r.Kind); k != "" {
		for _, t := range tokenize(utils.PluralizeKind(k)) {
			tokens[t] += 2.6
		}
	}
	for _, t := range tokenize(r.Group) {
		tokens[t] += 0.8
	}
	for _, t := range tokenize(r.Version) {
		tokens[t] += 0.8
	}
	for _, t := range tokenize(r.Description) {
		tokens[t] += 1.2
	}
	for key, value := range r.Labels {
		for _, t := range tokenize(key) {
			tokens[t] += 1.2
		}
		for _, t := range tokenize(value) {
			tokens[t] += 1.2
		}
	}
	for key, value := range r.Annotations {
		for _, t := range tokenize(key) {
			tokens[t] += 1.0
		}
		for _, t := range tokenize(value) {
			tokens[t] += 1.0
		}
	}
	for _, keyword := range r.Keywords {
		for _, t := range tokenize(keyword) {
			tokens[t] += 1.5
		}
	}
	return tokens
}

func TestBuildTokensMatchesOldIndexBehavior(t *testing.T) {
	r := testResource("cluster-a", "apps", "v1", "Deployment", "ns-a", "alpha-deploy")
	r.Labels = map[string]string{"app": "web", "tier": "frontend"}
	r.Annotations = map[string]string{"owner": "team-x"}
	r.Description = "a sample deployment"
	r.Keywords = []string{"important"}

	pooled := buildTokens(r)
	got := make(map[string]float32, len(pooled))
	maps.Copy(got, pooled)
	putTokens(pooled)

	want := buildTokensReference(r)
	if len(got) != len(want) {
		t.Fatalf("token count mismatch: pooled=%d reference=%d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("token %q boost mismatch: pooled=%v reference=%v", k, got[k], v)
		}
	}
}

func TestTokensPoolReuseIsClean(t *testing.T) {
	r1 := testResource("c", "apps", "v1", "Deployment", "ns", "zzz-unique-name")
	m := buildTokens(r1)
	if _, ok := m["zzz"]; !ok {
		t.Fatalf("expected token from r1")
	}
	putTokens(m)

	r2 := testResource("c", "", "v1", "Pod", "ns", "pod-a")
	m2 := buildTokens(r2)
	if _, ok := m2["zzz"]; ok {
		t.Fatalf("pooled map leaked tokens from a previous resource")
	}
	putTokens(m2)
}

func TestIndexConcurrentDoesNotCorrupt(t *testing.T) {
	idx := NewMemoryIndex()
	var wg sync.WaitGroup
	for c := range 8 {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := range 200 {
				r := testResource("cluster", "apps", "v1", "Deployment", "ns",
					"deploy-"+string(rune('a'+c))+"-"+string(rune('a'+(i%26))))
				if err := idx.Index(r); err != nil {
					t.Errorf("index: %v", err)
					return
				}
			}
		}(c)
	}
	wg.Wait()
	res, err := idx.Search(SearchQuery{Text: "deploy", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected results after concurrent indexing")
	}
}
