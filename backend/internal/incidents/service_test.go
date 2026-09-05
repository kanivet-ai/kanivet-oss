package incidents

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kanivet/backend/internal/db"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	d := &db.DB{DB: gdb}
	if err := d.MigrateEvents(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

func TestService_GetTimeline_FromDB(t *testing.T) {
	d := newTestDB(t)
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "web-0", "BackOff", "Warning", "back-off", 3, now),
		mkEvent(2, "Pod", "default", "web-0", "BackOff", "Warning", "back-off", 2, now.Add(time.Minute)),
		mkEvent(3, "Pod", "default", "web-1", "Pulled", "Normal", "pulled", 1, now),
	}
	for i := range events {
		events[i].ID = 0
	}
	if err := d.StoreEvents("c1", events); err != nil {
		t.Fatalf("store: %v", err)
	}

	svc := NewService(d, nil)
	resp, err := svc.GetTimeline(context.Background(), TimelineFilters{Cluster: "c1", IncludeRoutine: false, Limit: 100})
	if err != nil {
		t.Fatalf("GetTimeline: %v", err)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1 (routine filtered)", len(resp.Entries))
	}
	if resp.Entries[0].Count != 5 {
		t.Fatalf("count: got %d want 5", resp.Entries[0].Count)
	}
	if resp.Summary.Routine != 1 {
		t.Fatalf("routine summary: got %d want 1", resp.Summary.Routine)
	}
}

func TestService_GetTimeline_RequiresCluster(t *testing.T) {
	svc := NewService(newTestDB(t), nil)
	_, err := svc.GetTimeline(context.Background(), TimelineFilters{})
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}
}

func TestService_GetTimeline_IsolatesByCluster(t *testing.T) {
	d := newTestDB(t)
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	if err := d.StoreEvents("c1", []db.K8sEvent{mkEvent(0, "Pod", "default", "a", "BackOff", "Warning", "x", 1, now)}); err != nil {
		t.Fatalf("store c1: %v", err)
	}
	if err := d.StoreEvents("c2", []db.K8sEvent{mkEvent(0, "Pod", "default", "b", "BackOff", "Warning", "y", 1, now)}); err != nil {
		t.Fatalf("store c2: %v", err)
	}
	svc := NewService(d, nil)
	resp, err := svc.GetTimeline(context.Background(), TimelineFilters{Cluster: "c1", IncludeRoutine: true})
	if err != nil {
		t.Fatalf("GetTimeline: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "a" {
		t.Fatalf("cluster isolation failed: %+v", resp.Entries)
	}
}
