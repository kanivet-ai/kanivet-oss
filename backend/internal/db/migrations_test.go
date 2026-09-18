package db

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newMigratedTestDB(t *testing.T) *DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	d := &DB{gdb}
	if err := d.MigrateSearch(); err != nil {
		t.Fatalf("migrate search: %v", err)
	}
	if err := d.MigrateEvents(); err != nil {
		t.Fatalf("migrate events: %v", err)
	}
	if err := d.MigrateSnapshots(); err != nil {
		t.Fatalf("migrate snapshots: %v", err)
	}
	return d
}

func indexExists(t *testing.T, d *DB, name string) bool {
	t.Helper()
	var n int64
	d.Raw("SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", name).Scan(&n)
	return n > 0
}

func countRows(t *testing.T, d *DB, table string) int64 {
	t.Helper()
	var n int64
	d.Raw("SELECT count(*) FROM " + table).Scan(&n)
	return n
}

// A database from an older release carries gorm's default-named indexes next
// to the idx_sr_* ones and rows nobody has refreshed in months. The first
// start after updating must drop the duplicates, purge the stale rows, record
// the schema version, and do none of it again.
func TestVersionedMigrationV1(t *testing.T) {
	d := newMigratedTestDB(t)
	for _, idx := range legacySearchIndexes {
		col := idx[len("idx_searchable_resources_"):]
		if err := d.Exec("CREATE INDEX " + idx + " ON searchable_resources(" + col + ")").Error; err != nil {
			t.Fatalf("create legacy index %s: %v", idx, err)
		}
	}
	now := time.Now()
	rows := []SearchableResource{
		{ResourceID: "fresh", Cluster: "c1", Kind: "Pod", Name: "a", Namespace: "ns", IndexedAt: now},
		{ResourceID: "stale", Cluster: "c1", Kind: "Pod", Name: "b", Namespace: "ns", IndexedAt: now.Add(-searchRowRetention - time.Hour)},
		{ResourceID: "legacy-zero-time", Cluster: "c1", Kind: "Pod", Name: "c", Namespace: "ns"},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatalf("seed search rows: %v", err)
	}
	events := []K8sEvent{
		{Cluster: "c1", UID: "e-fresh", CreatedAt: now},
		{Cluster: "c1", UID: "e-old", CreatedAt: now.Add(-eventRetention - time.Hour)},
	}
	if err := d.Create(&events).Error; err != nil {
		t.Fatalf("seed events: %v", err)
	}

	if err := d.runVersionedMigrations(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, idx := range legacySearchIndexes {
		if indexExists(t, d, idx) {
			t.Errorf("legacy index %s still present", idx)
		}
	}
	if !indexExists(t, d, "idx_sr_natural_key") {
		t.Error("current natural key index must survive")
	}
	if got := countRows(t, d, "searchable_resources"); got != 1 {
		t.Errorf("searchable_resources rows = %d, want 1 (only the fresh row)", got)
	}
	if got := countRows(t, d, "k8s_events"); got != 1 {
		t.Errorf("k8s_events rows = %d, want 1", got)
	}
	if v := d.currentSchemaVersion(); v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}

	// Re-running is a no-op: a fresh stale row planted after the migration
	// must survive until the next maintenance pass, proving the step did not
	// run twice.
	if err := d.Create(&SearchableResource{ResourceID: "stale2", Cluster: "c1", Kind: "Pod", Name: "d", Namespace: "ns", IndexedAt: now.Add(-2 * searchRowRetention)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.runVersionedMigrations(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := countRows(t, d, "searchable_resources"); got != 2 {
		t.Errorf("second run must not purge again, rows = %d, want 2", got)
	}
}

// Clusters that vanished from every kubeconfig lose their cached rows only
// after a grace period, vclusters follow their host, and an empty keep list
// never purges anything.
func TestPurgeClustersNotIn(t *testing.T) {
	d := newMigratedTestDB(t)
	now := time.Now()
	old := now.Add(-absentClusterGrace - time.Hour)
	rows := []SearchableResource{
		{ResourceID: "kept", Cluster: "host-a", Kind: "Pod", Name: "a", Namespace: "ns", IndexedAt: old},
		{ResourceID: "kept-vc", Cluster: "vcluster:host-a:team:dev", Kind: "Pod", Name: "b", Namespace: "ns", IndexedAt: old},
		{ResourceID: "gone-old", Cluster: "gone", Kind: "Pod", Name: "c", Namespace: "ns", IndexedAt: old},
		{ResourceID: "gone-recent", Cluster: "gone-recent", Kind: "Pod", Name: "d", Namespace: "ns", IndexedAt: now},
		{ResourceID: "gone-vc", Cluster: "vcluster:gone:team:dev", Kind: "Pod", Name: "e", Namespace: "ns", IndexedAt: old},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&K8sEvent{Cluster: "gone", UID: "x", CreatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.SaveListSnapshot("items:gone::v1:pods:", "gone", []byte("[]")); err != nil {
		t.Fatal(err)
	}
	// A fresh snapshot would count as a recent write and keep the cluster,
	// so age it along with the rest of the cluster's rows.
	if err := d.Exec("UPDATE list_snapshots SET updated_at = ? WHERE cluster = 'gone'", old.Unix()).Error; err != nil {
		t.Fatal(err)
	}
	// A recent write in any table keeps every table's rows for that cluster.
	if err := d.SaveListSnapshot("items:gone-recent-snap::v1:pods:", "gone-recent-snap", []byte("[]")); err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&SearchableResource{ResourceID: "gone-recent-snap", Cluster: "gone-recent-snap", Kind: "Pod", Name: "f", Namespace: "ns", IndexedAt: old}).Error; err != nil {
		t.Fatal(err)
	}

	purged, removed, err := d.PurgeClustersNotIn(nil, now)
	if err != nil || removed != 0 || len(purged) != 0 {
		t.Fatalf("empty keep must purge nothing, got purged=%v removed=%d err=%v", purged, removed, err)
	}

	purged, removed, err = d.PurgeClustersNotIn([]string{"host-a"}, now)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if want := "[gone vcluster:gone:team:dev]"; fmtSlice(purged) != want {
		t.Fatalf("purged clusters = %v, want %s", purged, want)
	}
	if removed < 3 {
		t.Fatalf("removed = %d, want at least the 2 search rows and 1 event", removed)
	}
	var remaining []string
	d.Raw("SELECT resource_id FROM searchable_resources ORDER BY resource_id").Scan(&remaining)
	if got := fmtSlice(remaining); got != "[gone-recent gone-recent-snap kept kept-vc]" {
		t.Fatalf("remaining rows = %s", got)
	}
	if countRows(t, d, "k8s_events") != 0 {
		t.Fatal("events for the purged cluster must be removed")
	}
	var snapClusters []string
	d.Raw("SELECT cluster FROM list_snapshots ORDER BY cluster").Scan(&snapClusters)
	if got := fmtSlice(snapClusters); got != "[gone-recent-snap]" {
		t.Fatalf("snapshots must be removed for purged clusters only, remaining = %s", got)
	}
}

func TestParseSQLiteTime(t *testing.T) {
	d := newMigratedTestDB(t)
	want := time.Date(2026, 9, 18, 16, 4, 5, 123000000, time.UTC)
	if err := d.Create(&SearchableResource{ResourceID: "r", Cluster: "c", Kind: "Pod", Name: "n", Namespace: "ns", IndexedAt: want}).Error; err != nil {
		t.Fatal(err)
	}
	var raw string
	d.Raw("SELECT MAX(indexed_at) FROM searchable_resources").Scan(&raw)
	got, ok := parseSQLiteTime(raw)
	if !ok || !got.Equal(want) {
		t.Fatalf("parseSQLiteTime(%q) = %v, %v; want %v", raw, got, ok, want)
	}
	if _, ok := parseSQLiteTime("not a time"); ok {
		t.Fatal("garbage must not parse")
	}
}

func fmtSlice(s []string) string {
	out := "["
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out + "]"
}
