package search

import (
	"log"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/db"
)

// BatchWriter persists index changes in batches. Upserts and deletes share
// one queue so they reach the database in the order they happened: a delete
// drops any upsert still pending for the same ID, and a flush applies the
// pending deletes before the pending upserts (any upsert left pending for a
// deleted ID was added after its delete).
type BatchWriter struct {
	upsert         func([]db.SearchableResource) error
	deleteIDs      func([]string) error
	pendingWrites  []db.SearchableResource
	pendingDeletes map[string]struct{}
	mu             sync.Mutex
	flushChan      chan struct{}
	stopChan       chan struct{}
	stopOnce       sync.Once
	wg             sync.WaitGroup
}

func NewBatchWriter(database *db.DB) *BatchWriter {
	return newBatchWriter(database.BatchUpsertSearchableResources, database.DeleteSearchableResources, 2*time.Second)
}

func newBatchWriter(upsert func([]db.SearchableResource) error, deleteIDs func([]string) error, interval time.Duration) *BatchWriter {
	bw := &BatchWriter{
		upsert:         upsert,
		deleteIDs:      deleteIDs,
		pendingWrites:  make([]db.SearchableResource, 0, 500),
		pendingDeletes: make(map[string]struct{}),
		flushChan:      make(chan struct{}, 1),
		stopChan:       make(chan struct{}),
	}
	bw.wg.Add(1)
	go bw.flusher(interval)
	return bw
}

func (bw *BatchWriter) Add(resource db.SearchableResource) {
	bw.mu.Lock()
	bw.pendingWrites = append(bw.pendingWrites, resource)
	shouldFlush := len(bw.pendingWrites) >= 500
	bw.mu.Unlock()

	if shouldFlush {
		bw.kick()
	}
}

// Delete queues the removal of the rows with the given resource IDs and drops
// the upserts still pending for them, so a write queued before the delete
// cannot bring the row back.
func (bw *BatchWriter) Delete(ids ...string) {
	if len(ids) == 0 {
		return
	}
	bw.mu.Lock()
	for _, id := range ids {
		bw.pendingDeletes[id] = struct{}{}
	}
	if len(bw.pendingWrites) > 0 {
		kept := bw.pendingWrites[:0]
		for _, r := range bw.pendingWrites {
			if _, deleted := bw.pendingDeletes[r.ResourceID]; !deleted {
				kept = append(kept, r)
			}
		}
		clear(bw.pendingWrites[len(kept):])
		bw.pendingWrites = kept
	}
	shouldFlush := len(bw.pendingDeletes) >= 500
	bw.mu.Unlock()

	if shouldFlush {
		bw.kick()
	}
}

func (bw *BatchWriter) kick() {
	select {
	case bw.flushChan <- struct{}{}:
	default:
	}
}

func (bw *BatchWriter) flusher(interval time.Duration) {
	defer bw.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-bw.stopChan:
			bw.flush()
			return
		case <-ticker.C:
			bw.flush()
		case <-bw.flushChan:
			bw.flush()
		}
	}
}

// flush runs only on the flusher goroutine, so batches never overlap: a
// delete queued while an earlier batch is being written lands after it.
func (bw *BatchWriter) flush() {
	bw.mu.Lock()
	if len(bw.pendingWrites) == 0 && len(bw.pendingDeletes) == 0 {
		bw.mu.Unlock()
		return
	}
	toWrite := bw.pendingWrites
	bw.pendingWrites = make([]db.SearchableResource, 0, 500)
	var toDelete []string
	if len(bw.pendingDeletes) > 0 {
		toDelete = make([]string, 0, len(bw.pendingDeletes))
		for id := range bw.pendingDeletes {
			toDelete = append(toDelete, id)
		}
		bw.pendingDeletes = make(map[string]struct{})
	}
	bw.mu.Unlock()

	if len(toDelete) > 0 {
		if err := bw.deleteIDs(toDelete); err != nil {
			log.Printf("[SEARCH] Failed to batch delete %d resources: %v", len(toDelete), err)
		}
	}
	if len(toWrite) > 0 {
		if err := bw.upsert(toWrite); err != nil {
			log.Printf("[SEARCH] Failed to batch write %d resources: %v", len(toWrite), err)
		}
	}
}

func (bw *BatchWriter) Stop() {
	bw.stopOnce.Do(func() { close(bw.stopChan) })
	bw.wg.Wait()
}
