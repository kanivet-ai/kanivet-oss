package cluster

import (
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s"
)

type statusManagerTestClient struct {
	k8s.MockClient

	clusters []k8s.ClusterInfo
	delay    time.Duration

	mu        sync.Mutex
	calls     []string
	active    int
	maxActive int
}

func (c *statusManagerTestClient) ListClusters() ([]k8s.ClusterInfo, error) {
	return c.clusters, nil
}

func (c *statusManagerTestClient) GetClusterStatus(cluster string) (*k8s.ClusterStatus, error) {
	c.mu.Lock()
	c.calls = append(c.calls, cluster)
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.mu.Unlock()

	if c.delay > 0 {
		time.Sleep(c.delay)
	}

	c.mu.Lock()
	c.active--
	c.mu.Unlock()

	return &k8s.ClusterStatus{Name: cluster, Healthy: true}, nil
}

func (c *statusManagerTestClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *statusManagerTestClient) maxConcurrency() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxActive
}

func TestRefreshNextClusterStaggersStatusChecks(t *testing.T) {
	client := &statusManagerTestClient{
		clusters: []k8s.ClusterInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}},
	}
	manager := NewStatusManager(client)

	manager.refreshNextCluster()
	manager.refreshNextCluster()
	manager.refreshNextCluster()
	manager.refreshNextCluster()

	if got := client.callCount(); got != 3 {
		t.Fatalf("expected one status check per cluster before freshness cutoff, got %d", got)
	}
	if status := manager.GetStatus("a"); status == nil || !status.Healthy {
		t.Fatalf("expected cluster a status to be cached")
	}
	if status := manager.GetStatus("b"); status == nil || !status.Healthy {
		t.Fatalf("expected cluster b status to be cached")
	}
	if status := manager.GetStatus("c"); status == nil || !status.Healthy {
		t.Fatalf("expected cluster c status to be cached")
	}
}

func TestFetchAllStatusesLimitsConcurrency(t *testing.T) {
	client := &statusManagerTestClient{
		clusters: []k8s.ClusterInfo{
			{Name: "a"},
			{Name: "b"},
			{Name: "c"},
			{Name: "d"},
			{Name: "e"},
		},
		delay: 10 * time.Millisecond,
	}
	manager := NewStatusManager(client)

	manager.fetchAllStatuses()

	if got := client.callCount(); got != len(client.clusters) {
		t.Fatalf("expected all clusters to refresh, got %d", got)
	}
	if got := client.maxConcurrency(); got > statusFullRefreshParallel {
		t.Fatalf("expected concurrency <= %d, got %d", statusFullRefreshParallel, got)
	}
}

func TestRefreshNextClusterKeepsExistingStatusesWhenNewClusterIsFirst(t *testing.T) {
	client := &statusManagerTestClient{
		clusters: []k8s.ClusterInfo{{Name: "a"}, {Name: "b"}},
	}
	manager := NewStatusManager(client)

	manager.refreshNextCluster()
	manager.refreshNextCluster()

	client.clusters = []k8s.ClusterInfo{{Name: "new"}, {Name: "a"}, {Name: "b"}}
	manager.refreshNextCluster()

	if status := manager.GetStatus("a"); status == nil {
		t.Fatalf("expected cluster a status to remain cached")
	}
	if status := manager.GetStatus("b"); status == nil {
		t.Fatalf("expected cluster b status to remain cached")
	}
	if status := manager.GetStatus("new"); status == nil {
		t.Fatalf("expected new cluster status to be cached")
	}
}
