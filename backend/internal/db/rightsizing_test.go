package db

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRightsizingDismissalsKeyOnVirtualNamespace(t *testing.T) {
	d := newTestDB(t)
	if err := d.MigrateRightsizing(); err != nil {
		t.Fatal(err)
	}
	for _, vns := range []string{"apps", "jobs"} {
		if err := d.SaveRightsizingDismissal(&RightsizingDismissal{Cluster: "c", Namespace: "host", VClusterNamespace: vns, Kind: "Deployment", Name: "api", Reason: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := d.ListRightsizingDismissals("c")
	if err != nil || len(list) != 2 {
		t.Fatalf("want 2 dismissals, got %d (%v)", len(list), err)
	}
	if err := d.DeleteRightsizingDismissal("c", "host", "apps", "Deployment", "api", ""); err != nil {
		t.Fatal(err)
	}
	list, _ = d.ListRightsizingDismissals("c")
	if len(list) != 1 || list[0].VClusterNamespace != "jobs" {
		t.Fatalf("restoring one virtual namespace must leave the other: %+v", list)
	}
}

func TestRightsizingReportRoundTrip(t *testing.T) {
	d := newTestDB(t)
	if err := d.MigrateRightsizing(); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRightsizingReport("k", "c", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRightsizingReport("k", "c", []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetRightsizingReport("k")
	if err != nil || string(got) != "two" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRightsizingChunks(t *testing.T) {
	d := newTestDB(t)
	if err := d.MigrateRightsizing(); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRightsizingChunk("k1", "c", time.Now().Unix(), []byte("day")); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRightsizingChunk("old", "c", time.Now().AddDate(0, 0, -60).Unix(), []byte("old")); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetRightsizingChunk("k1")
	if err != nil || string(got) != "day" {
		t.Fatalf("got %q %v", got, err)
	}
	d.PruneRightsizingChunks()
	if _, err := d.GetRightsizingChunk("old"); err == nil {
		t.Fatal("a 60-day-old chunk should be pruned")
	}
	if _, err := d.GetRightsizingChunk("k1"); err != nil {
		t.Fatal("a fresh chunk must stay")
	}
}

// Pruned history must give its space back: a cache that only ever grows on
// the user's disk is the thing it exists to avoid.
func TestRightsizingCacheFileShrinksAfterPrune(t *testing.T) {
	d := newTestDB(t)
	path := filepath.Join(t.TempDir(), "cache", "rightsizing.db")
	if err := d.OpenRightsizingCache(path); err != nil {
		t.Fatal(err)
	}
	// Registered after TempDir, so it runs before the directory is removed:
	// on Windows an open file can't be deleted.
	t.Cleanup(func() { _ = d.CloseRightsizingCache() })
	if err := d.MigrateRightsizing(); err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 100<<10)
	old := time.Now().AddDate(0, 0, -45).Unix()
	for i := range 100 {
		_, _ = rand.Read(blob)
		if err := d.SaveRightsizingChunk(fmt.Sprint("k", i), "c", old, blob); err != nil {
			t.Fatal(err)
		}
	}
	size := func() int64 {
		d.rsCache.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	before := size()
	d.PruneRightsizingChunks()
	after := size()
	if before < 9<<20 || after > before/10 {
		t.Fatalf("cache file %d bytes before pruning, %d after", before, after)
	}
}

func TestForgetRightsizingClusters(t *testing.T) {
	d := newTestDB(t)
	if err := d.MigrateRightsizing(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"a", "b"} {
		if err := d.SaveRightsizingReport("report-"+c, c, []byte(c)); err != nil {
			t.Fatal(err)
		}
		if err := d.SaveRightsizingChunk("chunk-"+c, c, time.Now().Unix(), []byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ForgetRightsizingClusters(func(c string) bool { return c == "a" }); err != nil {
		t.Fatal(err)
	}
	if got, err := d.GetRightsizingReport("report-a"); err == nil {
		t.Fatalf("report for a survived: %q", got)
	}
	if got, err := d.GetRightsizingChunk("chunk-a"); err == nil {
		t.Fatalf("history for a survived: %q", got)
	}
	if got, err := d.GetRightsizingReport("report-b"); err != nil || string(got) != "b" {
		t.Fatalf("report for b must stay: %q, %v", got, err)
	}
	if got, err := d.GetRightsizingChunk("chunk-b"); err != nil || string(got) != "b" {
		t.Fatalf("history for b must stay: %q, %v", got, err)
	}
}
