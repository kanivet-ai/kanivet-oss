package search

import (
	"log"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/db"
)

type BatchWriter struct {
	db            *db.DB
	pendingWrites []db.SearchableResource
	mu            sync.Mutex
	flushChan     chan struct{}
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

func NewBatchWriter(database *db.DB) *BatchWriter {
	bw := &BatchWriter{
		db:            database,
		pendingWrites: make([]db.SearchableResource, 0, 500),
		flushChan:     make(chan struct{}, 1),
		stopChan:      make(chan struct{}),
	}
	bw.wg.Add(1)
	go bw.flusher()
	return bw
}

func (bw *BatchWriter) Add(resource db.SearchableResource) {
	bw.mu.Lock()
	bw.pendingWrites = append(bw.pendingWrites, resource)
	shouldFlush := len(bw.pendingWrites) >= 500
	bw.mu.Unlock()

	if shouldFlush {
		select {
		case bw.flushChan <- struct{}{}:
		default:
		}
	}
}

func (bw *BatchWriter) flusher() {
	defer bw.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
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

func (bw *BatchWriter) flush() {
	bw.mu.Lock()
	if len(bw.pendingWrites) == 0 {
		bw.mu.Unlock()
		return
	}
	toWrite := bw.pendingWrites
	bw.pendingWrites = make([]db.SearchableResource, 0, 500)
	bw.mu.Unlock()

	if err := bw.db.BatchUpsertSearchableResources(toWrite); err != nil {
		log.Printf("[SEARCH] Failed to batch write %d resources: %v", len(toWrite), err)
	}
}

func (bw *BatchWriter) Stop() {
	close(bw.stopChan)
	bw.wg.Wait()
}
