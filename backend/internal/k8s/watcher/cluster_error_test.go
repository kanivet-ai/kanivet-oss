package watcher

import (
	"sync"
	"testing"
	"time"
)

// allHub records the messages sent to every connection.
type allHub struct {
	captureBroadcaster
	allMu sync.Mutex
	all   []Message
}

func (h *allHub) BroadcastAll(m Message) error {
	h.allMu.Lock()
	defer h.allMu.Unlock()
	h.all = append(h.all, m)
	return nil
}

func (h *allHub) clusterErrors() int {
	h.allMu.Lock()
	defer h.allMu.Unlock()
	n := 0
	for _, m := range h.all {
		if ce, ok := m.(*ClusterErrorMessage); ok && ce.MessageType == "cluster_error" {
			n++
		}
	}
	return n
}

func newClusterErrorService() (*Service, *allHub) {
	hub := &allHub{}
	s := newServiceForResync(hub)
	s.clusterErr = make(map[string]*clusterErrState)
	return s, hub
}

// An unreachable cluster takes 30-60 s to fail each attempt. The banner used
// to need three failures within 30 s of the first, which such a cluster never
// produced, so it was never shown.
func TestTransientErrorBannerShowsForASlowlyFailingCluster(t *testing.T) {
	s, hub := newClusterErrorService()

	s.sendClusterErrorWithDetails("c", "timeout", "timed out", "", true)
	if hub.clusterErrors() != 0 {
		t.Fatal("the first failure must not raise the banner")
	}
	// The next attempt fails a minute later, without a success in between.
	s.clusterErrMu.Lock()
	s.clusterErr["c"].firstAt = time.Now().Add(-time.Minute)
	s.clusterErrMu.Unlock()
	s.sendClusterErrorWithDetails("c", "timeout", "timed out", "", true)
	if hub.clusterErrors() != 1 {
		t.Fatalf("%d banners after two failures a minute apart, want 1", hub.clusterErrors())
	}
}

// A blip, every topic of the cluster failing at once and then recovering,
// still raises no banner.
func TestTransientErrorBannerIgnoresABlip(t *testing.T) {
	s, hub := newClusterErrorService()
	for range 10 {
		s.sendClusterErrorWithDetails("c", "connection_failed", "refused", "", true)
	}
	s.markClusterHealthy("c")
	s.sendClusterErrorWithDetails("c", "connection_failed", "refused", "", true)
	if n := hub.clusterErrors(); n != 0 {
		t.Fatalf("%d banners for a blip, want none", n)
	}
	s.clusterErrMu.Lock()
	failures := s.clusterErr["c"].failures
	s.clusterErrMu.Unlock()
	if failures != 1 {
		t.Fatalf("%d failures counted after the cluster answered, want the count started over", failures)
	}
}
