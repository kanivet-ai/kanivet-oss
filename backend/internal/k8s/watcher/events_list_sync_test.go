package watcher

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kanivet/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func mkEventObj(name string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Event",
		"metadata": map[string]interface{}{
			"name":              name,
			"namespace":         "ns",
			"uid":               "uid-" + name,
			"resourceVersion":   "7",
			"creationTimestamp": "2026-01-01T00:00:00Z",
		},
		"reason":         "Pulled",
		"involvedObject": map[string]interface{}{"kind": "Pod", "name": "p", "namespace": "ns"},
	}}
}

// The events list used to come from the event listener's store: rows kept for
// 30 days whether or not the events still existed, counted as the whole list,
// then a watch from resourceVersion 0 replayed every live event on top. A
// relist stale-diffed the live rows against the stored ones and deleted them.
func TestEventsListSyncListsTheClusterNotTheStore(t *testing.T) {
	g, err := gorm.Open(sqlite.Open("file:"+filepath.Join(t.TempDir(), "kanivet.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Registered after TempDir, so it runs before the directory is removed:
	// on Windows an open file can't be deleted.
	t.Cleanup(func() { _ = sqlDB.Close() })
	database := &db.DB{DB: g}
	if err := database.MigrateEvents(); err != nil {
		t.Fatal(err)
	}
	if err := database.StoreEvents("cluster-a", []db.K8sEvent{{UID: "uid-expired", Name: "expired", Namespace: "ns", EventTime: time.Now().Add(-20 * 24 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}

	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	s.db = database
	lister := &fakeListLister{}
	for i := 0; i < 3; i++ {
		lister.items = append(lister.items, mkEventObj(fmt.Sprintf("live-%d", i)))
	}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "events"}
	topic := "items:cluster-a::v1:events:ns"

	rv, err := s.fetchAndBroadcastListSync(context.Background(), "cluster-a", gvr, "ns", topic, lister)
	if err != nil {
		t.Fatal(err)
	}
	if rv != "42" {
		t.Fatalf("watch would start from RV %q, want the list's 42", rv)
	}
	syncs := hub.syncCompletes()
	if len(syncs) != 1 || syncs[0].ItemCount != 3 {
		t.Fatalf("sync_complete = %+v, want one with 3 items", syncs)
	}
	for _, item := range s.cache.GetAll(topic) {
		if item["name"] == "expired" {
			t.Fatal("an expired stored event is in the list")
		}
	}

	if _, err := s.fetchAndBroadcastListSync(context.Background(), "cluster-a", gvr, "ns", topic, lister); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, m := range hub.batched {
		if ev, ok := m.(*ResourceEventMessage); ok && ev.Action == "deleted" {
			t.Errorf("relist deleted live event %v", ev.Item["name"])
		}
	}
	if got := s.cache.Count(topic); got != 3 {
		t.Fatalf("%d cached events after relist, want 3", got)
	}
}
