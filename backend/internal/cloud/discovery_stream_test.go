package cloud

import (
	"context"
	"testing"
	"time"
)

// After the consumer cancels and stops reading, the producer must return
// instead of blocking on a full channel for good.
func TestDiscoveryProducerStopsWhenConsumerIsGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan DiscoveryEvent) // nobody reads

	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Service{}).DiscoverClustersStreaming(ctx, DiscoverRequest{Provider: "bogus"}, ch)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("producer blocked on a channel nobody reads")
	}
	if _, open := <-ch; open {
		t.Fatal("channel was not closed")
	}
}

func TestEmitEventDeliversWhileConsumerReads(t *testing.T) {
	ch := make(chan DiscoveryEvent, 1)
	if !emitEvent(context.Background(), ch, DiscoveryEvent{Type: DiscoveryEventComplete}) {
		t.Fatal("event not sent into a free buffer")
	}
	if ev := <-ch; ev.Type != DiscoveryEventComplete {
		t.Fatalf("got %v", ev.Type)
	}
}

func TestEmitEventGivesUpOnFullChannelAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan DiscoveryEvent, 1)
	ch <- DiscoveryEvent{}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	done := make(chan bool, 1)
	go func() { done <- emitEvent(ctx, ch, DiscoveryEvent{}) }()
	select {
	case sent := <-done:
		if sent {
			t.Fatal("reported a send into a full channel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("emitEvent blocked after cancel")
	}
}
