package storage

import (
	"fmt"
	"strings"
	"testing"
)

// filler indexes n documents that share nothing with the queries below.
func filler(t testing.TB, idx *MemoryIndex, n int) {
	t.Helper()
	nss := []string{"payments", "checkout", "platform", "observability", "data"}
	kinds := []string{"Pod", "Deployment", "Service", "ConfigMap"}
	docs := make([]SearchableResource, 0, n)
	for i := 0; i < n; i++ {
		docs = append(docs, testResource("c1", "", "v1", kinds[i%len(kinds)], nss[i%len(nss)], fmt.Sprintf("checkout-api-%x-%x", i*7919, i)))
	}
	if err := idx.BatchIndex(docs); err != nil {
		t.Fatal(err)
	}
}

func resultNames(res []SearchResult) map[string]bool {
	out := make(map[string]bool, len(res))
	for _, r := range res {
		out[r.Resource.Name] = true
	}
	return out
}

// Name prefixes are indexed up to six characters. A longer partial word used
// to find no posting list and fall back to materializing and scoring every
// document in the index; it must be served from its six-character prefix.
func TestSearchLongPartialWordIsServedFromPrefixPostings(t *testing.T) {
	idx := NewMemoryIndex()
	filler(t, idx, 5000)
	for _, name := range []string{"recommendations-a1", "recommendations-b2", "shop-recommender", "unrelated-recommx"} {
		if err := idx.Index(testResource("c1", "", "v1", "Pod", "ml", name)); err != nil {
			t.Fatal(err)
		}
	}

	res, err := idx.Search(SearchQuery{Text: "recommend", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	got := resultNames(res)
	for _, want := range []string{"recommendations-a1", "recommendations-b2", "shop-recommender"} {
		if !got[want] {
			t.Errorf("%q missing from results %v", want, got)
		}
	}
	if got["unrelated-recommx"] {
		t.Errorf("a document sharing only the six-character prefix matched: %v", got)
	}

	allocs := testing.AllocsPerRun(5, func() {
		_, _ = idx.Search(SearchQuery{Text: "recommend", Limit: 20})
	})
	if allocs > 2000 {
		t.Errorf("long partial word allocated %.0f times per search over 5000 documents; it should not touch every document", allocs)
	}
}

// A query nothing in the term index knows still scans for mid-word substrings,
// but documents that cannot match must not be materialized and scored.
func TestSearchScanFallbackSkipsDocumentsThatCannotMatch(t *testing.T) {
	idx := NewMemoryIndex()
	filler(t, idx, 5000)
	if err := idx.Index(testResource("c1", "", "v1", "Pod", "ml", "shop-xrecommendx-1")); err != nil {
		t.Fatal(err)
	}

	res, err := idx.Search(SearchQuery{Text: "ecommend", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !resultNames(res)["shop-xrecommendx-1"] {
		t.Fatalf("mid-word query did not reach the scan fallback: %v", resultNames(res))
	}

	allocs := testing.AllocsPerRun(5, func() {
		_, _ = idx.Search(SearchQuery{Text: "zzqqxxjj", Limit: 20})
	})
	if allocs > 500 {
		t.Errorf("a query matching nothing allocated %.0f times per search over 5000 documents", allocs)
	}
}

// Fuzzy scoring is skipped for candidates that cannot reach the requested page.
// The page must still be exactly the head of the unlimited ranking.
func TestSearchPageMatchesHeadOfFullRanking(t *testing.T) {
	idx := NewMemoryIndex()
	filler(t, idx, 2000)
	extra := []SearchableResource{
		testResource("c1", "apps", "v1", "Deployment", "production", "deployer"),
		testResource("c1", "apps", "v1", "Deployment", "prod", "web"),
		testResource("c1", "", "v1", "Service", "deploymnet", "svc"),
		testResource("c1", "", "v1", "Pod", "kube-system", "checkout-ap"),
		{ID: "kind:c1::v1:Deployment", Cluster: "c1", Kind: "KindDefinition", Name: "Deployment", Version: "v1"},
		{ID: "kind:c1::v1:DeploymentConfig", Cluster: "c1", Kind: "KindDefinition", Name: "DeploymentConfig", Version: "v1"},
	}
	for _, r := range extra {
		if err := idx.Index(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{"deploymnet", "deployment", "checkout-api", "checkout", "prod", "payment", "pod", "observabilty"} {
		full, err := idx.Search(SearchQuery{Text: q})
		if err != nil {
			t.Fatal(err)
		}
		for _, page := range []struct{ offset, limit int }{{0, 1}, {0, 7}, {3, 5}, {0, 20}} {
			got, err := idx.Search(SearchQuery{Text: q, Offset: page.offset, Limit: page.limit})
			if err != nil {
				t.Fatal(err)
			}
			want := paginate(full, page.offset, page.limit)
			if len(got) != len(want) {
				t.Fatalf("%q page %+v: %d results, want %d", q, page, len(got), len(want))
			}
			for i := range want {
				if got[i].Score != want[i].Score {
					t.Fatalf("%q page %+v: result %d scored %v, want %v", q, page, i, got[i].Score, want[i].Score)
				}
			}
		}
	}
}

// Shards return only their first offset+limit results; the merged page must
// equal what one index holding every document returns.
func TestShardedSearchPageMatchesSingleIndex(t *testing.T) {
	s := NewShardedIndex(4)
	single := NewMemoryIndex()
	for c := 0; c < 6; c++ {
		cluster := fmt.Sprintf("cluster-%d", c)
		for i := 0; i < 300; i++ {
			r := testResource(cluster, "apps", "v1", "Deployment", fmt.Sprintf("ns-%d", i%7), fmt.Sprintf("deploy-%d-%d", c, i*31%997))
			_ = s.Index(r)
			_ = single.Index(r)
		}
		// Kind definitions come first and are capped across shards, so a
		// shard's page holds fewer regular results than the merged page.
		for k := 0; k < 3; k++ {
			name := fmt.Sprintf("Deploy%sSet", strings.Repeat("x", c+k))
			r := SearchableResource{ID: "kind:" + cluster + ":" + name, Cluster: cluster, Kind: "KindDefinition", Name: name, Version: "v1"}
			_ = s.Index(r)
			_ = single.Index(r)
		}
	}
	for _, q := range []string{"deploy", "deploy-3", "ns-4", "deplyo"} {
		for _, page := range []struct{ offset, limit int }{{0, 20}, {10, 15}, {0, 3}} {
			a, _ := s.Search(SearchQuery{Text: q, Offset: page.offset, Limit: page.limit})
			b, _ := single.Search(SearchQuery{Text: q, Offset: page.offset, Limit: page.limit})
			if len(a) != len(b) {
				t.Fatalf("%q page %+v: sharded %d results, single %d", q, page, len(a), len(b))
			}
			for i := range a {
				if a[i].Score != b[i].Score {
					t.Fatalf("%q page %+v: result %d scored %v sharded, %v single", q, page, i, a[i].Score, b[i].Score)
				}
			}
		}
	}
}
