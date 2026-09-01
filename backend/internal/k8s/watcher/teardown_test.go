package watcher

import (
	"testing"
)

func TestStopAllForClusterReapsOnlyIdleWatchesOfThatCluster(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	s.indexedClusters = map[string]bool{"cluster-a": true, "cluster-b": true}
	activeA := "items:cluster-a::v1:pods:"
	idleA := "items:cluster-a:apps:v1:deployments:"
	activeB := "items:cluster-b::v1:pods:"
	for _, topic := range []string{activeA, idleA, activeB} {
		registerWatch(t, s, topic)
		s.cache.Set(topic, map[string]interface{}{"name": "x", "namespace": "ns"})
	}
	s.manager.StopWatch(idleA)

	s.StopAllForCluster("cluster-a")

	if s.manager.HasWatch(idleA) {
		t.Fatal("idle grace-pending watch must be reaped on cluster release")
	}
	if got := len(s.cache.GetAll(idleA)); got != 0 {
		t.Fatalf("idle watch cache must be cleared, still has %d items", got)
	}
	if !s.manager.HasWatch(activeA) {
		t.Fatal("actively subscribed watch must survive a racing cluster release")
	}
	if len(s.cache.GetAll(activeA)) != 1 {
		t.Fatal("actively subscribed watch cache must survive")
	}
	if !s.manager.HasWatch(activeB) {
		t.Fatal("other cluster's watch must survive")
	}

	s.indexedClustersMu.Lock()
	indexed := s.indexedClusters["cluster-a"]
	s.indexedClustersMu.Unlock()
	if indexed {
		t.Fatal("cluster must be forgotten so reopening re-triggers kind indexing")
	}
}
