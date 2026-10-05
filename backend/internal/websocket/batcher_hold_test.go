package websocket

import (
	"testing"
	"time"
)

func noBatch(t *testing.T, s *countingSink, within time.Duration) {
	t.Helper()
	select {
	case n := <-s.counts:
		t.Fatalf("a batch of %d went out while the topic was held", n)
	case <-time.After(within):
	}
}

// A resync holds its topic: a change on a quiet topic, which would otherwise
// go out at once, waits for the resync's FlushTopic, and one queued after it
// goes out once the hold is released.
func TestHeldTopicWaitsForFlushTopic(t *testing.T) {
	const interval = 100 * time.Millisecond
	sink := &countingSink{counts: make(chan int, 16)}
	eb := NewEventBatcher(sink, interval, 100)
	defer eb.Shutdown()

	eb.AddEvent("t", mod("warmup", "1"))
	nextBatch(t, sink, 2*interval)
	time.Sleep(interval + 20*time.Millisecond)

	release := eb.HoldTopic("t")
	eb.AddEvent("t", mod("pod-a", "2"))
	noBatch(t, sink, 3*interval)

	if err := eb.FlushTopic("t"); err != nil {
		t.Fatal(err)
	}
	if n := nextBatch(t, sink, interval); n != 1 {
		t.Fatalf("FlushTopic sent %d events, want 1", n)
	}

	eb.AddEvent("t", mod("pod-b", "1"))
	noBatch(t, sink, 2*interval)
	release()
	release() // a second call is a no-op
	if n := nextBatch(t, sink, 2*interval); n != 1 {
		t.Fatalf("release sent %d events, want 1", n)
	}

	// Released, the topic batches as before.
	time.Sleep(interval + 20*time.Millisecond)
	start := time.Now()
	eb.AddEvent("t", mod("pod-c", "1"))
	nextBatch(t, sink, 2*interval)
	if waited := time.Since(start); waited > interval/2 {
		t.Fatalf("an event after the hold was released waited %v", waited)
	}
}
