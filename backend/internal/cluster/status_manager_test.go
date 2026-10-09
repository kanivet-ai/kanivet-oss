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

// scriptedStatusClient answers each status check with the next status in turn,
// repeating the last.
type scriptedStatusClient struct {
	k8s.MockClient
	answers []k8s.ClusterStatus
	checks  int
}

func (c *scriptedStatusClient) GetClusterStatus(cluster string) (*k8s.ClusterStatus, error) {
	answer := c.answers[min(c.checks, len(c.answers)-1)]
	c.checks++
	answer.Name = cluster
	return &answer, nil
}

func TestRefreshClusterChecksAgainAfterARefusedCredential(t *testing.T) {
	// The first check after a sign-in still carries the credential issued
	// before it; being refused is what makes the plugin run again.
	client := &scriptedStatusClient{answers: []k8s.ClusterStatus{
		{ErrorCode: "unauthorized", Error: "Authentication failed."},
		{Healthy: true},
	}}
	manager := NewStatusManager(client)

	status := manager.RefreshCluster("a")

	if status == nil || !status.Healthy {
		t.Fatalf("status after a sign-in = %+v, want healthy", status)
	}
	if client.checks != 2 {
		t.Fatalf("cluster checked %d times, want 2", client.checks)
	}
	if cached := manager.GetStatus("a"); cached == nil || !cached.Healthy {
		t.Fatalf("cached status = %+v, want the second answer", cached)
	}
}

func TestRefreshClusterChecksOnceOtherwise(t *testing.T) {
	for name, answer := range map[string]k8s.ClusterStatus{
		"healthy":                 {Healthy: true},
		"unreachable":             {Error: "Failed to get server version: dial tcp: i/o timeout"},
		"credential not obtained": {ErrorCode: "aws_sso_expired", Error: "Your AWS SSO session has expired."},
	} {
		t.Run(name, func(t *testing.T) {
			client := &scriptedStatusClient{answers: []k8s.ClusterStatus{answer}}
			status := NewStatusManager(client).RefreshCluster("a")
			if status == nil || status.Healthy != answer.Healthy || status.ErrorCode != answer.ErrorCode {
				t.Fatalf("status = %+v, want %+v", status, answer)
			}
			if client.checks != 1 {
				t.Fatalf("cluster checked %d times, want 1", client.checks)
			}
		})
	}
}

func TestRefreshClusterKeepsARefusalThatPersists(t *testing.T) {
	client := &scriptedStatusClient{answers: []k8s.ClusterStatus{{ErrorCode: "unauthorized", Error: "Authentication failed."}}}

	status := NewStatusManager(client).RefreshCluster("a")

	if status == nil || status.Healthy || status.ErrorCode != "unauthorized" {
		t.Fatalf("status = %+v, want the refusal", status)
	}
	if client.checks != 2 {
		t.Fatalf("cluster checked %d times, want 2", client.checks)
	}
}

func TestRefreshClusterAnnouncesAClusterThatAnswers(t *testing.T) {
	var reachable []string
	client := &scriptedStatusClient{answers: []k8s.ClusterStatus{
		{Error: "Failed to get server version: dial tcp: i/o timeout"},
		{Healthy: true},
	}}
	manager := NewStatusManager(client)
	manager.SetOnReachable(func(cluster string) { reachable = append(reachable, cluster) })

	manager.RefreshCluster("a")
	if len(reachable) != 0 {
		t.Fatalf("announced %v for a cluster that does not answer", reachable)
	}
	manager.RefreshCluster("a")
	if len(reachable) != 1 || reachable[0] != "a" {
		t.Fatalf("announced %v, want [a]", reachable)
	}
}

func TestBackgroundChecksDoNotAnnounceReachableClusters(t *testing.T) {
	announced := 0
	client := &statusManagerTestClient{clusters: []k8s.ClusterInfo{{Name: "a"}}}
	manager := NewStatusManager(client)
	manager.SetOnReachable(func(string) { announced++ })

	manager.refreshNextCluster()
	manager.RefreshAll()

	if client.callCount() == 0 {
		t.Fatal("the background checks did not run")
	}
	if announced != 0 {
		t.Fatalf("background checks announced a reachable cluster %d times", announced)
	}
}
