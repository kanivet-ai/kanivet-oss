package watcher

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kanivet/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newSnapshotDB(t *testing.T) *db.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Every connection to :memory: is a database of its own.
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	d := &db.DB{DB: gdb}
	if err := d.MigrateSnapshots(); err != nil {
		t.Fatalf("migrate snapshots: %v", err)
	}
	return d
}

// A list longer than the snapshot cap persists the rows the user sees first
// under the topic's sort, not whichever ones the cache map yields first.
func TestSaveSnapshotKeepsTheFirstRowsOfTheSort(t *testing.T) {
	s := newServiceForResync(&captureBroadcaster{sortBy: "name", sortOrder: "asc"})
	s.db = newSnapshotDB(t)
	topic := "items:c::v1:pods:"
	for i := maxSnapshotItems + 100 - 1; i >= 0; i-- {
		s.cache.Set(topic, map[string]interface{}{"name": fmt.Sprintf("p-%05d", i), "namespace": "ns"})
	}

	s.saveSnapshot(topic)

	data, err := s.db.GetListSnapshot(topic)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]interface{}
	if err := jsonv2.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != maxSnapshotItems {
		t.Fatalf("snapshot has %d rows, want %d", len(items), maxSnapshotItems)
	}
	for i, item := range items {
		if want := fmt.Sprintf("p-%05d", i); item["name"] != want {
			t.Fatalf("snapshot row %d is %v, want %s", i, item["name"], want)
		}
	}
}

func TestSaveSnapshotSoonSkipsRecentlySavedTopics(t *testing.T) {
	s := newServiceForResync(&captureBroadcaster{sortBy: "name", sortOrder: "asc"})
	s.db = newSnapshotDB(t)
	topic := "items:c::v1:pods:"

	s.saveSnapshotSoon(topic)
	first := s.snapshotSavedAt[topic]
	s.saveSnapshotSoon(topic)

	if first.IsZero() || !s.snapshotSavedAt[topic].Equal(first) {
		t.Fatalf("a relist right after a save must not write the snapshot again")
	}
}
