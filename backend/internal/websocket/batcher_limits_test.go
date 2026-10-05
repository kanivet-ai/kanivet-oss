package websocket

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

// frameSink records each broadcast frame's wire size and event count, and
// reports a configurable message limit like core.Hub does.
type frameSink struct {
	mu     sync.Mutex
	limit  int64
	sizes  []int
	counts []int
	block  chan struct{}
}

func (f *frameSink) MaxMessageSize() int64 { return f.limit }

func (f *frameSink) Broadcast(topic string, msg core.Message) error {
	if f.block != nil {
		<-f.block
	}
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	bm := msg.(*BatchedMessage)
	f.mu.Lock()
	f.sizes = append(f.sizes, len(data))
	f.counts = append(f.counts, bm.Count)
	f.mu.Unlock()
	if int64(len(data)) > f.limit {
		return core.ErrInvalidMessage
	}
	return nil
}

func bigEvent(uid string, size int) *seqMsg {
	return &seqMsg{action: "MODIFIED", item: map[string]interface{}{"uid": uid, "name": uid, "blob": strings.Repeat("x", size)}}
}

// A batch that outgrew the connection's message limit used to be rejected
// whole; it is now cut into frames that fit.
func TestFlushSplitsByBytes(t *testing.T) {
	sink := &frameSink{limit: 64 * 1024}
	eb := NewEventBatcher(sink, time.Hour, 100)
	defer eb.Shutdown()

	release := eb.HoldTopic("t")
	for i := 0; i < 40; i++ {
		if err := eb.AddEvent("t", bigEvent(fmt.Sprint(i), 5000)); err != nil {
			t.Fatal(err)
		}
	}
	release()
	if err := eb.FlushTopic("t"); err != nil {
		t.Fatalf("flush: %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	total := 0
	for i, s := range sink.sizes {
		if int64(s) > sink.limit {
			t.Fatalf("frame %d is %d bytes, over the %d limit", i, s, sink.limit)
		}
		total += sink.counts[i]
	}
	if total != 40 {
		t.Fatalf("delivered %d events, want 40", total)
	}
	if len(sink.sizes) < 2 {
		t.Fatalf("expected the batch to be split, got %d frame(s)", len(sink.sizes))
	}
}

// A topic held for a long time accumulates far more than maxBatchSize events;
// they go out in frames of at most maxBatchSize.
func TestFlushSplitsByCount(t *testing.T) {
	sink := &frameSink{limit: 10 << 20}
	eb := NewEventBatcher(sink, time.Hour, 100)
	defer eb.Shutdown()

	release := eb.HoldTopic("t")
	defer release()
	for i := 0; i < 250; i++ {
		_ = eb.AddEvent("t", bigEvent(fmt.Sprint(i), 10))
	}
	if err := eb.FlushTopic("t"); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	total := 0
	for _, c := range sink.counts {
		if c > 100 {
			t.Fatalf("frame carries %d events, want <= 100", c)
		}
		total += c
	}
	if total != 250 {
		t.Fatalf("delivered %d events, want 250", total)
	}
}

// One event that cannot fit any frame is skipped; its neighbours still go out.
func TestOversizedSingleEventIsSkipped(t *testing.T) {
	sink := &frameSink{limit: 32 * 1024}
	eb := NewEventBatcher(sink, time.Hour, 100)
	defer eb.Shutdown()

	release := eb.HoldTopic("t")
	_ = eb.AddEvent("t", bigEvent("a", 10))
	_ = eb.AddEvent("t", bigEvent("huge", 64*1024))
	_ = eb.AddEvent("t", bigEvent("b", 10))
	release()
	if err := eb.FlushTopic("t"); err != nil {
		t.Fatalf("flush: %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	total := 0
	for _, c := range sink.counts {
		total += c
	}
	if total != 2 {
		t.Fatalf("delivered %d events, want 2 (the huge one skipped)", total)
	}
}

// Once the cap is hit every further AddEvent used to spawn its own flush
// goroutine; at most one may be pending per batch.
func TestSizeTriggeredFlushSpawnsOneGoroutine(t *testing.T) {
	sink := &frameSink{limit: 10 << 20, block: make(chan struct{})}
	eb := NewEventBatcher(sink, time.Hour, 100)
	before := runtime.NumGoroutine()
	for i := 0; i < 1000; i++ {
		_ = eb.AddEvent("t", bigEvent(fmt.Sprint(i), 1))
	}
	if extra := runtime.NumGoroutine() - before; extra > 5 {
		t.Fatalf("%d extra goroutines after a 1000-event burst", extra)
	}
	close(sink.block)
	eb.Shutdown()
}

func TestAddEventAfterShutdownIsRejected(t *testing.T) {
	sink := &frameSink{limit: 10 << 20}
	eb := NewEventBatcher(sink, time.Hour, 100)
	eb.Shutdown()
	if err := eb.AddEvent("t", bigEvent("a", 1)); !errors.Is(err, ErrBatcherClosed) {
		t.Fatalf("AddEvent after Shutdown = %v, want ErrBatcherClosed", err)
	}
}

// Concurrent AddEvent past the cap and Shutdown must not race wg.Add/Wait.
func TestShutdownRacesWithSizeTriggeredFlush(t *testing.T) {
	for n := 0; n < 20; n++ {
		sink := &frameSink{limit: 10 << 20}
		eb := NewEventBatcher(sink, time.Hour, 100)
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 300; i++ {
					_ = eb.AddEvent(fmt.Sprintf("t%d", g), bigEvent(fmt.Sprint(i), 1))
				}
			}(g)
		}
		eb.Shutdown()
		wg.Wait()
		eb.Shutdown()
	}
}
