package k8s

import (
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// newProbeSupervisor builds a supervisor whose health loop runs against a
// scripted probe instead of a real tunnel. Results past the end of the script
// report healthy.
func newProbeSupervisor(t *testing.T, script []bool) (*VClusterSupervisor, *int32) {
	t.Helper()
	sup := NewVClusterSupervisor(nil, "vcluster:host:ns:vc", "host", "ns", "vc")
	sup.currentConfig = &rest.Config{Host: "https://localhost:1"}
	sup.state = VClusterStateHealthy
	sup.probeInterval = 2 * time.Millisecond
	var calls int32
	sup.probe = func(*rest.Config) bool {
		n := atomic.AddInt32(&calls, 1)
		if int(n) <= len(script) {
			return script[n-1]
		}
		return true
	}
	return sup, &calls
}

func waitForCalls(t *testing.T, calls *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(calls) < want {
		if time.Now().After(deadline) {
			t.Fatalf("probe called %d times, want at least %d", atomic.LoadInt32(calls), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// One failed probe followed by a success must leave the supervisor healthy.
// Before this was fixed the state stayed "reconnecting" forever, so every
// call to the vcluster waited out WaitHealthy's timeout and failed while the
// tunnel itself was fine.
func TestHealthLoopReturnsToHealthyAfterTransientProbeFailure(t *testing.T) {
	sup, calls := newProbeSupervisor(t, []bool{false, true, true})
	done := make(chan struct{})
	go func() { sup.runHealthLoop(); close(done) }()

	waitForCalls(t, calls, 3)
	// The state transition happens right after the probe returns; give the
	// loop one more tick to publish it.
	deadline := time.Now().Add(time.Second)
	for sup.Status().State != VClusterStateHealthy && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if st := sup.Status(); st.State != VClusterStateHealthy || st.Detail != "" {
		t.Fatalf("state after recovery = %q (%q), want healthy with no detail", st.State, st.Detail)
	}

	close(sup.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health loop did not stop")
	}
}

// Two consecutive failures must end the loop with the state left in
// reconnecting so run() tears the tunnel down and reconnects.
func TestHealthLoopExitsAfterTwoConsecutiveFailures(t *testing.T) {
	sup, _ := newProbeSupervisor(t, []bool{true, false, false})
	done := make(chan struct{})
	go func() { sup.runHealthLoop(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("health loop kept running after two consecutive failed probes")
	}
	if st := sup.Status().State; st != VClusterStateReconnecting {
		t.Fatalf("state after two failures = %q, want reconnecting", st)
	}
}
