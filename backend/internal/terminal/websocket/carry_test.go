package websocket

import (
	"testing"
	"unicode/utf8"
)

// Every cut of valid output, wherever the read ended, goes out as two valid
// UTF-8 messages that together are the original text. The end-to-end test
// coalesces reads that are already queued, so it cannot pin this down alone.
func TestUTF8CarryHoldsBackCutCharacters(t *testing.T) {
	text := []byte("a─┬─ é 🙂b")
	for cut := 0; cut <= len(text); cut++ {
		var c utf8Carry
		first := c.next(text[:cut])
		second := c.next(text[cut:])
		if !utf8.Valid(first) || !utf8.Valid(second) {
			t.Fatalf("cut at %d: sent %q then %q", cut, first, second)
		}
		if got := string(first) + string(second); got != string(text) {
			t.Fatalf("cut at %d: sent %q, want %q", cut, got, text)
		}
		if len(c.pending) != 0 {
			t.Fatalf("cut at %d: %q still held back", cut, c.pending)
		}
	}
}

// Only a sequence that could still become valid is held back; invalid bytes go
// out with the read they arrived in.
func TestUTF8CarryPassesInvalidBytesOn(t *testing.T) {
	for _, tc := range []struct {
		in        string
		out, held string
	}{
		{"ok \xff", "ok \xff", ""},
		{"ok \xe2\x94", "ok ", "\xe2\x94"},
		{"ok \xe0\x80", "ok \xe0\x80", ""},
		{"ok \x94\x94\x94\x94", "ok \x94\x94\x94\x94", ""},
	} {
		var c utf8Carry
		if out := c.next([]byte(tc.in)); string(out) != tc.out || string(c.pending) != tc.held {
			t.Errorf("next(%q) sent %q holding %q, want %q holding %q", tc.in, out, c.pending, tc.out, tc.held)
		}
	}
}
