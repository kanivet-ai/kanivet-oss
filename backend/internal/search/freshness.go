package search

import (
	"log"
	"sort"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/db"
)

// knownClustersTTL bounds how long the set of kubeconfig contexts is reused
// between searches; reading it walks every kubeconfig.
const knownClustersTTL = 30 * time.Second

// freshness tracks what this session has refreshed, so results served from
// the persisted index can say how old they are, and which clusters the
// current kubeconfigs still name.
type freshness struct {
	mu sync.Mutex
	// sessionStart is when the service started. A document indexed since
	// then came from this session's watch events or LIST sweeps.
	sessionStart time.Time
	// objectsAt and kindsAt record, per cluster, when this session last
	// completed a LIST sweep and a kind refresh.
	objectsAt map[string]time.Time
	kindsAt   map[string]time.Time

	// listClusters returns the contexts of the current kubeconfigs.
	listClusters func() ([]string, error)
	known        map[string]struct{}
	knownAt      time.Time
}

func (f *freshness) markObjects(cluster string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.objectsAt == nil {
		f.objectsAt = make(map[string]time.Time)
	}
	f.objectsAt[cluster] = at
}

func (f *freshness) markKinds(cluster string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kindsAt == nil {
		f.kindsAt = make(map[string]time.Time)
	}
	f.kindsAt[cluster] = at
}

func (f *freshness) forget(cluster string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objectsAt, cluster)
	delete(f.kindsAt, cluster)
}

// knownClusters returns the kubeconfig contexts, or nil when they cannot be
// read or none are configured: an empty answer almost always means an
// unreadable kubeconfig, not that every cluster is gone.
func (f *freshness) knownClusters(now time.Time) map[string]struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listClusters == nil {
		return nil
	}
	if f.known != nil && now.Sub(f.knownAt) < knownClustersTTL {
		return f.known
	}
	names, err := f.listClusters()
	if err != nil || len(names) == 0 {
		if err != nil {
			log.Printf("[SEARCH] Could not read kubeconfig contexts, not filtering results by cluster: %v", err)
		}
		return f.known
	}
	known := make(map[string]struct{}, len(names))
	for _, n := range names {
		known[n] = struct{}{}
	}
	f.known, f.knownAt = known, now
	return known
}

func clusterKept(known map[string]struct{}, cluster string) bool {
	if _, ok := known[cluster]; ok {
		return true
	}
	if host, ok := db.VClusterHost(cluster); ok {
		_, ok = known[host]
		return ok
	}
	return false
}

// restrictToKnownClusters limits a search to clusters the current kubeconfigs
// name. Rows for a removed context are kept for days in case its kubeconfig
// is only temporarily unreadable, and must not show up as live results
// meanwhile. It returns the clusters to search (nil: all) and false when none
// of the requested clusters is known.
func (s *Service) restrictToKnownClusters(requested []string) ([]string, bool) {
	known := s.freshness.knownClusters(s.nowFn())
	if known == nil {
		return requested, true
	}
	if len(requested) > 0 {
		kept := make([]string, 0, len(requested))
		for _, c := range requested {
			if clusterKept(known, c) {
				kept = append(kept, c)
			}
		}
		return kept, len(kept) > 0
	}
	indexed := s.index.Clusters()
	allKnown := !s.dbHasUnloaded.Load()
	for _, c := range indexed {
		if !clusterKept(known, c) {
			allKnown = false
			break
		}
	}
	if allKnown {
		return nil, true
	}
	set := make(map[string]struct{}, len(known)+len(indexed))
	for c := range known {
		set[c] = struct{}{}
	}
	for _, c := range indexed {
		if clusterKept(known, c) {
			set[c] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, true
}

// keepKnownClusters drops results of clusters the kubeconfigs no longer name.
func (s *Service) keepKnownClusters(results []SearchResult) []SearchResult {
	known := s.freshness.knownClusters(s.nowFn())
	if known == nil {
		return results
	}
	out := results[:0:0]
	for _, r := range results {
		if clusterKept(known, r.Resource.Cluster) {
			out = append(out, r)
		}
	}
	return out
}

// annotateFreshness returns a copy of results with IndexedAt and Stale set.
// A result is stale when its document was not indexed this session and this
// session has not yet refreshed its cluster: it was loaded from the persisted
// index and may describe an object that has since changed or gone.
func (s *Service) annotateFreshness(results []SearchResult) []SearchResult {
	if len(results) == 0 {
		return results
	}
	f := &s.freshness
	f.mu.Lock()
	sessionStart := f.sessionStart
	objectsAt, kindsAt := f.objectsAt, f.kindsAt
	out := make([]SearchResult, len(results))
	for i, r := range results {
		at := r.Resource.UpdatedAt
		if !at.IsZero() {
			r.IndexedAt = at.UnixMilli()
		}
		refreshed := objectsAt
		if r.Resource.Kind == "KindDefinition" {
			refreshed = kindsAt
		}
		_, clusterRefreshed := refreshed[r.Resource.Cluster]
		r.Stale = !clusterRefreshed && !sessionStart.IsZero() && at.Before(sessionStart)
		out[i] = r
	}
	f.mu.Unlock()
	return out
}
