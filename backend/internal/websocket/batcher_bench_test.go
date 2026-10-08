package websocket

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkBatcherAddEventParallel(b *testing.B) {
	eb := NewEventBatcher(&recordingBroadcaster{}, 250*time.Millisecond, 100)
	defer eb.Shutdown()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			topic := fmt.Sprintf("items:c:g:v:k%d:", i%8)
			_ = eb.AddEvent(topic, &seqMsg{action: "MODIFIED", item: map[string]interface{}{"uid": fmt.Sprint(i % 5000), "name": "n"}})
		}
	})
}

func BenchmarkBatcherFlush100(b *testing.B) {
	eb := NewEventBatcher(&recordingBroadcaster{}, time.Hour, 100)
	defer eb.Shutdown()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 100; j++ {
			_ = eb.AddEvent("t", &seqMsg{action: "MODIFIED", item: map[string]interface{}{"uid": fmt.Sprint(j), "name": "n"}})
		}
		_ = eb.FlushTopic("t")
	}
}
