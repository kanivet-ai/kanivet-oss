package search

import (
	"testing"
	"time"

	"github.com/kanivet/backend/internal/search/storage"
)

func deployDoc(cluster, name string, at time.Time) storage.SearchableResource {
	return storage.SearchableResource{
		ID: storage.BuildResourceID(cluster, "apps", "v1", "deployments", "ns", name), Cluster: cluster, Kind: "Deployment",
		APIVersion: "apps/v1", Group: "apps", Version: "v1", Name: name, Namespace: "ns", Category: "Workloads", UpdatedAt: at,
	}
}

func resultClusters(results []SearchResult) map[string]SearchResult {
	out := map[string]SearchResult{}
	for _, r := range results {
		if r.Resource.Kind != "KindDefinition" {
			out[r.Resource.Cluster] = r
		}
	}
	return out
}

// Rows of a context removed from the kubeconfig are kept for a week, and
// rows of clusters not refreshed this session can be days old; neither may
// look like live results.
func TestSearchHidesRemovedClustersAndMarksStaleResults(t *testing.T) {
	s := newTestSearchService()
	s.index = storage.NewShardedIndex(storage.DefaultSearchShards)
	now := time.Now()
	s.freshness.sessionStart = now.Add(-time.Minute).Truncate(time.Second)
	s.freshness.listClusters = func() ([]string, error) { return []string{"prod", "dev"}, nil }
	lastWeek := now.Add(-7 * 24 * time.Hour)
	for _, d := range []storage.SearchableResource{
		deployDoc("prod", "api", lastWeek),
		deployDoc("dev", "api", now),
		deployDoc("vcluster:prod:team:vc1", "api", lastWeek),
		deployDoc("removed", "api", lastWeek),
	} {
		if err := s.index.Index(d); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.Search("api", SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	got := resultClusters(results)
	if _, ok := got["removed"]; ok {
		t.Fatal("a cluster missing from the kubeconfig is searched")
	}
	if len(got) != 3 {
		t.Fatalf("results from %v, want prod, dev and the prod vcluster", got)
	}
	if r := got["prod"]; !r.Stale || r.IndexedAt != lastWeek.Unix()*1000 {
		t.Fatalf("prod result from last session: stale=%v indexedAt=%d, want stale at %d", r.Stale, r.IndexedAt, lastWeek.Unix()*1000)
	}
	if got["dev"].Stale {
		t.Fatal("a document indexed this session is marked stale")
	}

	// Once this session's sweep of prod completes, its results are current.
	s.freshness.markObjects("prod", now)
	results, err = s.Search("api", SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if resultClusters(results)["prod"].Stale {
		t.Fatal("prod is still stale after this session refreshed it")
	}

	// Asking for the removed cluster alone finds nothing.
	results, err = s.Search("api", SearchOptions{Clusters: []string{"removed"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d results from a removed cluster", len(results))
	}
}

// An unreadable kubeconfig must not hide every result.
func TestSearchKeepsAllClustersWhenKubeconfigIsUnreadable(t *testing.T) {
	s := newTestSearchService()
	s.freshness.listClusters = func() ([]string, error) { return nil, nil }
	if err := s.index.Index(deployDoc("prod", "api", time.Now())); err != nil {
		t.Fatal(err)
	}
	results, err := s.Search("api", SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resultClusters(results)["prod"]; !ok {
		t.Fatal("results hidden while the kubeconfig could not be read")
	}
}
