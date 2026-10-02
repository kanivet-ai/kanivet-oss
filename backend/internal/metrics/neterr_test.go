package metrics

import (
	"fmt"
	"net"
	"os"
	"testing"
)

// The platform's own socket errors, wrapped the way net/http returns them,
// must count as a dead port-forward on every OS, Windows included.
func TestPlatformSocketErrorsMeanDeadPortForward(t *testing.T) {
	for name, errno := range map[string]error{"refused": errnoConnRefused, "reset": errnoConnReset, "aborted": errnoConnAborted} {
		err := fmt.Errorf("failed to query mimir: %w", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)})
		if !isPortForwardLikelyDead(err) {
			t.Errorf("%s (%v) not treated as a dead port-forward", name, err)
		}
	}
	if !IsConnRefused(&net.OpError{Err: os.NewSyscallError("connect", errnoConnRefused)}) {
		t.Error("refused not recognised")
	}
	if !IsConnReset(&net.OpError{Err: os.NewSyscallError("read", errnoConnReset)}) {
		t.Error("reset not recognised")
	}
}
