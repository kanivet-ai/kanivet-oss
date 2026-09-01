package search

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/search/storage"
)

func TestMemoryIndexConcurrentReadsDuringIndexing(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(prevProcs)

	idx := storage.NewMemoryIndex()
	idx.SetEvictionDisabled(true)

	watched := storage.SearchableResource{
		ID:         "c//v1/pods/ns/web",
		Cluster:    "c",
		Kind:       "Pod",
		APIVersion: "v1",
		Name:       "web",
		Namespace:  "ns",
		Category:   "Workloads",
		Version:    "v1",
		Keywords:   []string{"web", "pod"},
		CreatedAt:  time.Unix(1, 0),
		UpdatedAt:  time.Unix(1, 0),
	}
	if err := idx.Index(watched); err != nil {
		t.Fatalf("prime watched resource: %v", err)
	}

	const readers = 6
	const writers = 3

	deadline := time.Now().Add(2 * time.Second)
	start := make(chan struct{})
	errCh := make(chan error, 1)

	var readOps atomic.Uint64
	var writeOps atomic.Uint64
	var nextKind atomic.Uint64
	var failed atomic.Bool
	var wg sync.WaitGroup

	fail := func(format string, args ...interface{}) {
		if failed.CompareAndSwap(false, true) {
			errCh <- fmt.Errorf(format, args...)
		}
	}

	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for !failed.Load() && time.Now().Before(deadline) {
				if !idx.HasDocument(watched.ID) {
					fail("watched document disappeared from HasDocument")
					return
				}
				doc, ok := idx.GetDocument(watched.ID)
				if !ok || doc.ID != watched.ID {
					fail("GetDocument lost watched document: ok=%v id=%q", ok, doc.ID)
					return
				}
				doc, ok = idx.GetDocumentWithWarmTier(watched.ID)
				if !ok || doc.ID != watched.ID {
					fail("GetDocumentWithWarmTier lost watched document: ok=%v id=%q", ok, doc.ID)
					return
				}
				if count := idx.DocumentCount(); count < 1 {
					fail("DocumentCount dropped below 1: %d", count)
					return
				}
				readOps.Add(1)
			}
		}()
	}

	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for !failed.Load() && time.Now().Before(deadline) {
				n := nextKind.Add(1)
				kind := fmt.Sprintf("Kind-%d", n)
				if err := idx.Index(storage.SearchableResource{
					ID:          fmt.Sprintf("kind:c:apps:v1:%s", kind),
					Cluster:     "c",
					Kind:        "KindDefinition",
					Name:        kind,
					Category:    "Kind",
					APIVersion:  "v1",
					Group:       "apps",
					Version:     "v1",
					Description: kind + " resource kind",
					Keywords:    []string{kind},
					CreatedAt:   time.Unix(0, int64(n)),
					UpdatedAt:   time.Unix(0, int64(n)),
				}); err != nil {
					fail("index kind definition %s: %v", kind, err)
					return
				}
				writeOps.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}

	if readOps.Load() == 0 || writeOps.Load() == 0 {
		t.Fatalf("workload did not run: reads=%d writes=%d", readOps.Load(), writeOps.Load())
	}
}
