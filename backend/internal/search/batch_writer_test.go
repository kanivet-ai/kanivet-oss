package search

import (
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/search/storage"
)

// fakeRows stands in for the searchable_resources table.
type fakeRows struct {
	mu   sync.Mutex
	rows map[string]bool
}

func (f *fakeRows) upsert(rs []db.SearchableResource) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rs {
		f.rows[r.ResourceID] = true
	}
	return nil
}

func (f *fakeRows) delete(ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		delete(f.rows, id)
	}
	return nil
}

func (f *fakeRows) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[id]
}

// A delete used to run at once while the upsert queued just before it waited
// up to 2s for the next flush, which then inserted the deleted row again.
func TestDeleteIsNotUndoneByAPendingUpsert(t *testing.T) {
	rows := &fakeRows{rows: map[string]bool{}}
	bw := newBatchWriter(rows.upsert, rows.delete, time.Hour)
	h := &ResourceEventHandler{index: storage.NewShardedIndex(storage.DefaultSearchShards), batchWriter: bw}
	// persistResource only runs with a database; queue the upsert directly
	// the way it would.
	pod := map[string]interface{}{"name": "web-0", "namespace": "ns", "kind": "Pod", "apiVersion": "v1"}
	coords := resourceCoords{version: "v1", resource: "pods", kind: "Pod"}
	if _, err := h.OnAddWithCoords("c", coords, pod); err != nil {
		t.Fatal(err)
	}
	id := storage.BuildResourceID("c", "", "v1", "pods", "ns", "web-0")
	bw.Add(db.SearchableResource{ResourceID: id})
	if err := h.onDeleteWithCoords("c", coords, pod); err != nil {
		t.Fatal(err)
	}
	bw.Stop()
	if rows.has(id) {
		t.Fatal("deleted row was recreated by the upsert queued before the delete")
	}
}

func TestUpsertAfterDeleteWins(t *testing.T) {
	rows := &fakeRows{rows: map[string]bool{"x": true}}
	bw := newBatchWriter(rows.upsert, rows.delete, time.Hour)
	bw.Delete("x")
	bw.Add(db.SearchableResource{ResourceID: "x"})
	bw.Stop()
	if !rows.has("x") {
		t.Fatal("an object recreated after its delete must stay persisted")
	}
}

// Deleting the Deployment web used to fall back to removing every document
// named web in the namespace when the exact ID missed.
func TestDeleteFallbackKeepsOtherKindsWithTheSameName(t *testing.T) {
	idx := storage.NewShardedIndex(storage.DefaultSearchShards)
	h := &ResourceEventHandler{index: idx}
	svc := map[string]interface{}{"name": "web", "namespace": "ns", "kind": "Service", "apiVersion": "v1"}
	cm := map[string]interface{}{"name": "web", "namespace": "ns", "kind": "ConfigMap", "apiVersion": "v1"}
	for _, obj := range []map[string]interface{}{svc, cm} {
		if _, err := h.OnAdd("c", obj); err != nil {
			t.Fatal(err)
		}
	}
	// A legacy Deployment document whose ID carries the singular kind, so the
	// reconstructed ID misses and the fallback runs.
	if err := idx.Index(storage.SearchableResource{
		ID: "c/apps/v1/Deployment/ns/web", Cluster: "c", Kind: "Deployment", APIVersion: "apps/v1",
		Group: "apps", Version: "v1", Namespace: "ns", Name: "web",
	}); err != nil {
		t.Fatal(err)
	}
	del := map[string]interface{}{"name": "web", "namespace": "ns"}
	if err := h.onDeleteWithCoords("c", resourceCoords{group: "apps", version: "v1", resource: "deployments"}, del); err != nil {
		t.Fatal(err)
	}
	if idx.HasDocument("c/apps/v1/Deployment/ns/web") {
		t.Fatal("the Deployment survived its delete")
	}
	if n := idx.DocumentCount(); n != 2 {
		t.Fatalf("%d documents left, want the Service and the ConfigMap", n)
	}
	// Event deletes have nothing indexed to remove and must not touch others.
	ev := map[string]interface{}{"name": "web", "namespace": "ns", "kind": "Event", "apiVersion": "v1"}
	if err := h.onDeleteWithCoords("c", resourceCoords{version: "v1", resource: "events"}, ev); err != nil {
		t.Fatal(err)
	}
	if n := idx.DocumentCount(); n != 2 {
		t.Fatalf("an Event delete removed documents: %d left", n)
	}
}
