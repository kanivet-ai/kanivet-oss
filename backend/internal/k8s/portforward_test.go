package k8s

import (
	"testing"
	"time"
)

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
func TestListPortForwardsExcludesPrivateAndInactive(t *testing.T) {
	m := NewPortForwardManager(nil)
	m.mu.Lock()
	now := time.Now()
	m.forwards["a-ns-pod-8080"] = &PortForward{ID: "a-ns-pod-8080", Cluster: "a", Namespace: "ns", PodName: "pod", LocalPort: 9000, RemotePort: 8080, Active: true, CreatedAt: now}
	m.forwards["private#1"] = &PortForward{ID: "private#1", Cluster: "a", Namespace: "ns", PodName: "pod", LocalPort: 9001, RemotePort: 9090, Active: true, CreatedAt: now}
	m.forwards["b-ns-pod-80"] = &PortForward{ID: "b-ns-pod-80", Cluster: "b", Namespace: "ns", PodName: "pod", LocalPort: 9002, RemotePort: 80, Active: true, CreatedAt: now.Add(time.Second)}
	m.forwards["dead"] = &PortForward{ID: "dead", Cluster: "a", Namespace: "ns", PodName: "pod", LocalPort: 9003, RemotePort: 443, Active: false, CreatedAt: now}
	m.mu.Unlock()

	all := m.ListPortForwards("")
	if len(all) != 3 || all[0].ID != "b-ns-pod-80" {
		t.Fatalf("got %+v", all)
	}
	onlyA := m.ListPortForwards("a")
	if len(onlyA) != 2 {
		t.Fatalf("cluster filter got %+v", onlyA)
	}
}

func TestPrivatePortForwardIsNeverShared(t *testing.T) {
	m := NewPortForwardManager(nil)
	m.start = readyAtOnce

	owner, err := m.CreatePortForward("c", "monitoring", "prometheus-0", 9090, 0)
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
	again, err := m.CreatePortForward("c", "monitoring", "prometheus-0", 9090, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reused, err := m.CreatePortForward("c", "monitoring", "prometheus-0", 9090, 0); err != nil || reused != again {
		t.Fatalf("shared forward not reused: %v", err)
	}
	_ = m.StopPortForward(again.ID)
	_ = m.StopPortForward(pool.ID)
}
