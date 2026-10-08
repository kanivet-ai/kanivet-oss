package k8s

import "testing"

// readyAtOnce stands in for the SPDY tunnel: ready immediately, and it never
// consumes a stop signal, so the test can see who was told to stop.
func readyAtOnce(pf *PortForward) error {
	close(pf.readyChan)
	return nil
}

// A short-lived user of a target (the metrics detection probe) must not get
// the long-lived owner's forward back, and stopping its own must leave the
// owner's running. Before private forwards the probe was handed the owner's
// forward and stopped it on return.
func TestPrivatePortForwardIsNeverShared(t *testing.T) {
	m := NewPortForwardManager(nil)
	m.start = readyAtOnce

	owner, err := m.CreatePortForward("c", "monitoring", "prometheus-0", 9090)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := m.CreatePrivatePortForward("c", "monitoring", "prometheus-0", 9090)
	if err != nil {
		t.Fatal(err)
	}
	if probe == owner || probe.ID == owner.ID {
		t.Fatalf("private forward shares the owner's: %s", probe.ID)
	}
	if err := m.StopPortForward(probe.ID); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.GetPortForward(owner.ID); !ok || got != owner || !owner.Active {
		t.Fatal("stopping the private forward tore down the owner's")
	}
	select {
	case <-owner.stopChan:
		t.Fatal("the owner's forward was told to stop")
	default:
	}

	// The other way round: stopping the shared ID leaves a private one alone.
	pool, err := m.CreatePrivatePortForward("c", "monitoring", "prometheus-0", 9090)
	if err != nil {
		t.Fatal(err)
	}
	if pool.ID == probe.ID {
		t.Fatalf("two private forwards share ID %s", pool.ID)
	}
	if err := m.StopPortForward(owner.ID); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.GetPortForward(pool.ID); !ok || got != pool {
		t.Fatal("stopping the shared forward tore down a private one")
	}

	// Shared forwards keep their reuse semantics for the user's own API.
	again, err := m.CreatePortForward("c", "monitoring", "prometheus-0", 9090)
	if err != nil {
		t.Fatal(err)
	}
	if reused, err := m.CreatePortForward("c", "monitoring", "prometheus-0", 9090); err != nil || reused != again {
		t.Fatalf("shared forward not reused: %v", err)
	}
	_ = m.StopPortForward(again.ID)
	_ = m.StopPortForward(pool.ID)
}
