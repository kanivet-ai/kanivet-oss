package websocket

import (
	"fmt"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

// countingSink reports the size of every batch it is handed.
type countingSink struct{ counts chan int }

func (s *countingSink) Broadcast(topic string, msg core.Message) error {
	s.counts <- msg.(*BatchedMessage).Count
	return nil
}

func nextBatch(t *testing.T, s *countingSink, within time.Duration) int {
	t.Helper()
	select {
	case n := <-s.counts:
		return n
	case <-time.After(within):
		t.Fatalf("no batch within %v", within)
		return 0
	}
}

// A lone change on a topic that has been quiet for a whole interval (a pod
// turning Ready, the user's own scale reflected back) must not wait out the
// batching window.
func TestIsolatedEventAfterQuietIntervalIsNotDelayed(t *testing.T) {
	const interval = 300 * time.Millisecond
	sink := &countingSink{counts: make(chan int, 16)}
	eb := NewEventBatcher(sink, interval, 100)
	defer eb.Shutdown()

	eb.AddEvent("t", mod("pod-a", "1"))
	nextBatch(t, sink, 2*interval)
	time.Sleep(interval + 50*time.Millisecond)

	start := time.Now()
	eb.AddEvent("t", mod("pod-a", "2"))
	nextBatch(t, sink, 2*interval)
	if waited := time.Since(start); waited > interval/2 {
		t.Fatalf("isolated event after a quiet interval waited %v", waited)
	}
}

// Flushing the first event of a burst at once must not turn the rest of the
// burst into one message per event.
func TestBurstAfterQuietIntervalStillCoalesces(t *testing.T) {
	const interval = 200 * time.Millisecond
	sink := &countingSink{counts: make(chan int, 64)}
	eb := NewEventBatcher(sink, interval, 100)
	defer eb.Shutdown()

	eb.AddEvent("t", mod("warmup", "1"))
	nextBatch(t, sink, 2*interval)
	time.Sleep(interval + 50*time.Millisecond)

	for i := range 50 {
		eb.AddEvent("t", mod(fmt.Sprintf("pod-%02d", i), "1"))
	}
	delivered, batches := 0, 0
	for delivered < 50 {
		delivered += nextBatch(t, sink, 2*interval)
		batches++
	}
	if batches > 2 {
		t.Fatalf("a 50-event burst went out in %d batches, want at most 2", batches)
	}
}
