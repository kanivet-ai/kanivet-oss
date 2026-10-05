package transport

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/kanivet/backend/internal/websocket/core"
)

// A connection that fails every send must not log once per message.
func TestSendFailureLogIsRateLimited(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	_, conn, cleanup := dialOne(t, 8)
	defer cleanup()
	sm := NewSubscriptionManager()
	if _, err := sm.Subscribe("t", conn); err != nil {
		t.Fatal(err)
	}
	// Larger than the 1MB message limit: every send fails with ErrInvalidMessage.
	msg := core.NewOutgoingMessage("event", map[string]any{"x": strings.Repeat("a", 2<<20)})
	for i := 0; i < 200; i++ {
		_ = sm.Broadcast("t", msg)
	}
	if n := strings.Count(buf.String(), "[SubMgr] Send failed"); n != 1 {
		t.Fatalf("%d send-failure lines for 200 failed broadcasts, want 1", n)
	}
	if err := sm.UnsubscribeAll(conn); err != nil {
		t.Fatal(err)
	}
	sm.failLogMu.Lock()
	left := len(sm.failLog)
	sm.failLogMu.Unlock()
	if left != 0 {
		t.Fatalf("failLog keeps %d entries after UnsubscribeAll", left)
	}
}
