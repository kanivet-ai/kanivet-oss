package storage

import (
	"strings"
	"testing"
)

// TestPartialQueryStillRanksExpectedResource locks the user-facing contract that
// survives the prefix-token memory optimization: typing a partial word (2..6
// chars) must still surface the obviously-matching resource at the top. We
// dropped per-field prefix explosion from tokenize() and now emit prefixes only
// for Name and Kind; this test guards that partial search on those fields did
// not regress.
func TestPartialQueryStillRanksExpectedResource(t *testing.T) {
	idx := NewMemoryIndex()
	corpus := []SearchableResource{
		{ID: "1", Cluster: "c", Kind: "Deployment", Group: "apps", Version: "v1", Namespace: "prod", Name: "nginx-ingress-controller"},
		{ID: "2", Cluster: "c", Kind: "Service", Group: "", Version: "v1", Namespace: "prod", Name: "nginx-ingress"},
		{ID: "3", Cluster: "c", Kind: "ConfigMap", Group: "", Version: "v1", Namespace: "kube-system", Name: "coredns"},
		{ID: "4", Cluster: "c", Kind: "StatefulSet", Group: "apps", Version: "v1", Namespace: "db", Name: "postgres-primary"},
		{ID: "5", Cluster: "c", Kind: "Secret", Group: "", Version: "v1", Namespace: "prod", Name: "tls-cert"},
		{ID: "6", Cluster: "c", Kind: "DaemonSet", Group: "apps", Version: "v1", Namespace: "kube-system", Name: "fluent-bit"},
		{ID: "7", Cluster: "c", Kind: "Ingress", Group: "networking.k8s.io", Version: "v1", Namespace: "prod", Name: "web-ingress"},
		{ID: "8", Cluster: "c", Kind: "CronJob", Group: "batch", Version: "v1", Namespace: "ops", Name: "backup-runner"},
	}
	for _, r := range corpus {
		if err := idx.Index(r); err != nil {
			t.Fatalf("index %s: %v", r.ID, err)
		}
	}

	// query -> the resource ID that must appear in the top results.
	cases := []struct {
		query  string
		wantID string
		topN   int
	}{
		{"deployment", "1", 3}, // full kind
		{"deploy", "1", 3},     // partial kind
		{"depl", "1", 5},       // shorter partial kind
		{"statefulset", "4", 3},
		{"state", "4", 5}, // partial kind
		{"ingress", "7", 5},
		{"ingr", "7", 8}, // partial kind, multiple ingress-ish
		{"nginx", "1", 5},
		{"ngin", "1", 8},      // partial name
		{"postgres", "4", 3},  // name
		{"postg", "4", 5},     // partial name
		{"coredns", "3", 3},   // name
		{"core", "3", 5},      // partial name
		{"daemonset", "6", 3}, // full kind
		{"daem", "6", 5},      // partial kind
		{"cronjob", "8", 3},
		{"cron", "8", 5}, // partial kind
		{"secret", "5", 5},
		{"secr", "5", 8}, // partial kind
	}

	for _, tc := range cases {
		res, err := idx.Search(SearchQuery{Text: tc.query, Limit: 25})
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		found := -1
		for i, r := range res {
			if r.Resource.ID == tc.wantID {
				found = i
				break
			}
		}
		if found < 0 {
			ids := make([]string, 0, len(res))
			for _, r := range res {
				ids = append(ids, r.Resource.ID+":"+r.Resource.Kind+"/"+r.Resource.Name)
			}
			t.Errorf("query %q: expected resource %q NOT in results at all. Got: %s",
				tc.query, tc.wantID, strings.Join(ids, ", "))
			continue
		}
		if found >= tc.topN {
			t.Errorf("query %q: expected resource %q in top %d, but it ranked #%d",
				tc.query, tc.wantID, tc.topN, found+1)
		}
	}
}
