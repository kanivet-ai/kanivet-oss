package rightsizing

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/metrics"
)

// reportStore keeps persisted reports in memory.
type reportStore struct {
	mu      sync.Mutex
	reports map[string]string // key -> cluster
	data    map[string][]byte
}

func newReportStore() *reportStore {
	return &reportStore{reports: map[string]string{}, data: map[string][]byte{}}
}

func (s *reportStore) SaveRightsizingReport(key, cluster string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports[key], s.data[key] = cluster, data
	return nil
}

func (s *reportStore) GetRightsizingReport(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key], nil
}

func (s *reportStore) ForgetRightsizingClusters(match func(string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, cluster := range s.reports {
		if match(cluster) {
			delete(s.reports, key)
			delete(s.data, key)
		}
	}
	return nil
}

func (s *reportStore) ListRightsizingDismissals(string) ([]db.RightsizingDismissal, error) {
	return nil, nil
}
func (s *reportStore) SaveRightsizingDismissal(*db.RightsizingDismissal) error { return nil }
func (s *reportStore) DeleteRightsizingDismissal(_, _, _, _, _, _ string) error {
	return nil
}

func TestForgetClusterDropsReportsReadFromItsMetricsSource(t *testing.T) {
	host := t.Name()
	vc := "vcluster:" + host + ":vc-ns:vc1"
	other := t.Name() + "-other"
	st := newReportStore()
	s := NewService(nil, nil, nil, nil, st)
	now := time.Now()
	for _, c := range []string{host, vc, other} {
		key := runKey(c, ProfileBalanced, defaultWindow, "mimir")
		rep := &Report{Cluster: c, Status: StatusReady, ComputedAt: now}
		s.runs[key] = &run{cluster: c, ready: rep, readyAt: now, loaded: true}
		data, err := packReport(rep)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.SaveRightsizingReport(key, c, data)
	}
	probeCache.Store(host+"|provider=mimir", probeEntry{probe{cpuSeries: 1}, now})
	probeCache.Store(other+"|provider=mimir", probeEntry{probe{cpuSeries: 1}, now})

	s.ForgetCluster(host)

	for _, c := range []string{host, vc} {
		if s.Cached(c, ProfileBalanced, defaultWindow, "mimir") != nil {
			t.Errorf("%s: report for the old settings still served", c)
		}
		if data, _ := st.GetRightsizingReport(runKey(c, ProfileBalanced, defaultWindow, "mimir")); data != nil {
			t.Errorf("%s: persisted report would come back after a restart", c)
		}
	}
	if s.Cached(other, ProfileBalanced, defaultWindow, "mimir") == nil {
		t.Error("another cluster's report was dropped")
	}
	if data, _ := st.GetRightsizingReport(runKey(other, ProfileBalanced, defaultWindow, "mimir")); data == nil {
		t.Error("another cluster's persisted report was dropped")
	}
	if _, ok := probeCache.Load(host + "|provider=mimir"); ok {
		t.Error("signal probe for the old settings survived")
	}
	if _, ok := probeCache.Load(other + "|provider=mimir"); !ok {
		t.Error("another cluster's probe was dropped")
	}
	probeCache.Delete(other + "|provider=mimir")
}

// changingSource changes the metrics settings while a report is computing.
type changingSource struct {
	metricsSource
	during func()
}

func (s *changingSource) HistorySource(context.Context, string) (*metrics.ProviderInfo, bool) {
	s.during()
	return &metrics.ProviderInfo{Type: "mimir", Found: true, NeedsTenant: true}, false
}

func TestReportComputedBeforeSettingsChangeIsDropped(t *testing.T) {
	cluster := t.Name()
	key := runKey(cluster, ProfileBalanced, defaultWindow, "mimir")
	src := &changingSource{}
	s := NewService(nil, src, nil, nil, newReportStore())
	s.runs[key] = &run{cluster: cluster, computing: true, loaded: true}
	src.during = func() {
		s.ForgetCluster(cluster)
		// The UI polls again at once and starts a run for the new settings.
		s.mu.Lock()
		s.runs[key] = &run{cluster: cluster, computing: true, loaded: true}
		s.mu.Unlock()
	}

	s.computeAndStore(key, cluster, ProfileBalanced, defaultWindow, "mimir")

	s.mu.Lock()
	r := s.runs[key]
	s.mu.Unlock()
	if r.ready != nil {
		t.Fatalf("the new run got the old settings' report: %+v", r.ready)
	}
	if !r.computing {
		t.Fatal("the old computation finished the new run's computation")
	}
}

func TestReportKeptWhenSettingsDidNotChange(t *testing.T) {
	cluster := t.Name()
	key := runKey(cluster, ProfileBalanced, defaultWindow, "mimir")
	s := NewService(nil, &changingSource{during: func() {}}, nil, nil, nil)
	s.runs[key] = &run{cluster: cluster, computing: true, loaded: true}

	s.computeAndStore(key, cluster, ProfileBalanced, defaultWindow, "mimir")

	if rep := s.Cached(cluster, ProfileBalanced, defaultWindow, "mimir"); rep == nil || rep.Status != StatusNeedsTenant {
		t.Fatalf("report = %+v", rep)
	}
}
