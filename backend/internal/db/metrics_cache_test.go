package db

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestMetricsCachePersistenceLRUAndBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "metrics.db")
	d := &DB{}
	if err := d.OpenMetricsCache(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.CloseMetricsCache() })
	put := func(key string, value []byte, budget int64) {
		t.Helper()
		if err := d.SaveMetricsQuery(key, value, budget); err != nil {
			t.Fatal(err)
		}
	}
	put("a", []byte("1"), 260)
	put("b", []byte("2"), 260)
	if _, err := d.GetMetricsQuery("a"); err != nil {
		t.Fatal(err)
	}
	put("c", []byte("3"), 260)
	if _, err := d.GetMetricsQuery("b"); err == nil {
		t.Fatal("LRU entry not evicted")
	}
	put("huge", make([]byte, 300), 260)
	if err := d.CloseMetricsCache(); err != nil {
		t.Fatal(err)
	}
	if err := d.OpenMetricsCache(path); err != nil {
		t.Fatal(err)
	}
	if got, err := d.GetMetricsQuery("a"); err != nil || string(got) != "1" {
		t.Fatalf("restart lost data: %q, %v", got, err)
	}
	put("c", []byte("longer replacement"), 260)
	if _, err := d.GetMetricsQuery("a"); err == nil {
		t.Fatal("growing replacement exceeded budget without eviction")
	}
	var size int64
	d.metricsCache.Model(&MetricsQuery{}).Select("SUM(bytes)").Scan(&size)
	if size > 260 {
		t.Fatalf("cache grew to %d", size)
	}
}

func TestMetricsCacheConcurrentWritesStayBounded(t *testing.T) {
	d := &DB{}
	if err := d.OpenMetricsCache(filepath.Join(t.TempDir(), "metrics.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.CloseMetricsCache() })
	const budget = 8192
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprint(i)
			if err := d.SaveMetricsQuery(key, bytes.Repeat([]byte("x"), 1000), budget); err != nil {
				t.Error(err)
			}
			_, _ = d.GetMetricsQuery(key)
		}(i)
	}
	wg.Wait()
	var size int64
	if err := d.metricsCache.Model(&MetricsQuery{}).Select("SUM(bytes)").Scan(&size).Error; err != nil {
		t.Fatal(err)
	}
	if size > budget || size == 0 {
		t.Fatalf("concurrent cache size=%d", size)
	}
}

func TestRetireRightsizingChunksPreservesReportsAndDismissals(t *testing.T) {
	d := newTestDB(t)
	if err := d.MigrateRightsizing(); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRightsizingChunk("chunk", "cluster", 1, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRightsizingReport("report", "cluster", []byte("report")); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRightsizingDismissal(&RightsizingDismissal{Cluster: "cluster", Namespace: "apps", Kind: "Deployment", Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := d.RetireRightsizingChunks(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetRightsizingChunk("chunk"); err == nil {
		t.Fatal("legacy chunks not removed")
	}
	if _, err := d.GetRightsizingReport("report"); err != nil {
		t.Fatal(err)
	}
	if got, err := d.ListRightsizingDismissals("cluster"); err != nil || len(got) != 1 {
		t.Fatalf("dismissals=%v, %v", got, err)
	}
}
