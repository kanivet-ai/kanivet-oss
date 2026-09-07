package cluster

import (
	"context"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s"
)

const (
	statusRefreshPeriod       = 30 * time.Second
	statusRefreshMinSpacing   = 2 * time.Second
	statusMaxBackoff          = 5 * time.Minute
	statusFullRefreshParallel = 2
)

type StatusManager struct {
	k8s           k8s.Interface
	statuses      map[string]*k8s.ClusterStatus
	lastRefresh   map[string]time.Time
	backoff       map[string]time.Duration
	active        func() bool
	mu            sync.RWMutex
	refreshTicker *time.Ticker
	stopCh        chan struct{}
	stopped       bool
	wg            sync.WaitGroup
}

func NewStatusManager(k8sClient k8s.Interface) *StatusManager {
	return &StatusManager{
		k8s:         k8sClient,
		statuses:    make(map[string]*k8s.ClusterStatus),
		lastRefresh: make(map[string]time.Time),
		backoff:     make(map[string]time.Duration),
		stopCh:      make(chan struct{}),
	}
}

// SetActiveCheck installs a predicate consulted before each background probe;
// when it returns false (no UI connected) probing is paused entirely.
func (m *StatusManager) SetActiveCheck(fn func() bool) {
	m.mu.Lock()
	m.active = fn
	m.mu.Unlock()
}

func (m *StatusManager) Start(ctx context.Context) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.refreshTicker = time.NewTicker(statusRefreshMinSpacing)
		m.refreshNextCluster()
		for {
			select {
			case <-m.refreshTicker.C:
				m.refreshNextCluster()
			case <-m.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *StatusManager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	m.mu.Unlock()
	close(m.stopCh)
	if m.refreshTicker != nil {
		m.refreshTicker.Stop()
	}
	m.wg.Wait()
}

func (m *StatusManager) store(cluster string, status *k8s.ClusterStatus) {
	m.mu.Lock()
	m.statuses[cluster] = status
	if status.Healthy {
		delete(m.backoff, cluster)
	} else {
		m.backoff[cluster] = min(max(2*m.backoff[cluster], statusRefreshPeriod), statusMaxBackoff)
	}
	m.mu.Unlock()
}

func (m *StatusManager) refreshNextCluster() {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active != nil && !active() {
		return
	}
	clusters, err := m.k8s.ListClusters()
	if err != nil || len(clusters) == 0 {
		return
	}

	currentClusters := make(map[string]bool, len(clusters))
	var target string
	var earliest time.Time
	now := time.Now()

	m.mu.Lock()
	for _, cluster := range clusters {
		name := cluster.Name
		currentClusters[name] = true
		due := m.lastRefresh[name].Add(statusRefreshPeriod + m.backoff[name])
		if target == "" || due.Before(earliest) {
			target, earliest = name, due
		}
	}
	for name := range m.statuses {
		if !currentClusters[name] {
			delete(m.statuses, name)
			delete(m.lastRefresh, name)
			delete(m.backoff, name)
		}
	}
	if target == "" || earliest.After(now) {
		m.mu.Unlock()
		return
	}
	m.lastRefresh[target] = now
	m.mu.Unlock()

	if status, err := m.k8s.GetClusterStatus(target); err == nil {
		m.store(target, status)
	}
}

func (m *StatusManager) fetchAllStatuses() {
	clusters, err := m.k8s.ListClusters()
	if err != nil {
		return
	}
	currentClusters := make(map[string]bool, len(clusters))
	var wg sync.WaitGroup
	sem := make(chan struct{}, statusFullRefreshParallel)
	for _, cluster := range clusters {
		currentClusters[cluster.Name] = true
		wg.Add(1)
		go func(clusterName string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m.mu.Lock()
			m.lastRefresh[clusterName] = time.Now()
			m.mu.Unlock()
			if status, err := m.k8s.GetClusterStatus(clusterName); err == nil {
				m.store(clusterName, status)
			}
		}(cluster.Name)
	}
	wg.Wait()
	m.mu.Lock()
	for name := range m.statuses {
		if !currentClusters[name] {
			delete(m.statuses, name)
			delete(m.lastRefresh, name)
			delete(m.backoff, name)
		}
	}
	m.mu.Unlock()
}

func (m *StatusManager) GetStatus(cluster string) *k8s.ClusterStatus {
	m.mu.RLock()
	status := m.statuses[cluster]
	m.mu.RUnlock()
	return status
}

func (m *StatusManager) GetAllStatuses() map[string]*k8s.ClusterStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*k8s.ClusterStatus, len(m.statuses))
	for k, v := range m.statuses {
		result[k] = v
	}
	return result
}

func (m *StatusManager) RefreshCluster(cluster string) *k8s.ClusterStatus {
	m.mu.Lock()
	m.lastRefresh[cluster] = time.Now()
	delete(m.backoff, cluster)
	m.mu.Unlock()
	status, err := m.k8s.GetClusterStatus(cluster)
	if err != nil {
		return nil
	}
	m.store(cluster, status)
	return status
}

func (m *StatusManager) RefreshAll() {
	m.fetchAllStatuses()
}
