package api

import (
	"fmt"
	"testing"
)

func TestDetailTabStatesBounded(t *testing.T) {
	m := NewDetailTabManager()
	for i := 0; i < maxDetailTabStates+100; i++ {
		m.SetTabState("c", "apps", "v1", "Pod", "ns", fmt.Sprintf("pod-%d", i), "yaml")
	}
	m.mu.RLock()
	n := len(m.states)
	m.mu.RUnlock()
	if n > maxDetailTabStates {
		t.Fatalf("states grew to %d, cap is %d", n, maxDetailTabStates)
	}
	last := fmt.Sprintf("pod-%d", maxDetailTabStates+99)
	if got := m.GetTabState("c", "apps", "v1", "Pod", "ns", last); got.ActiveTab != "yaml" {
		t.Fatalf("most recent state evicted: %+v", got)
	}
	if got := m.GetTabState("c", "apps", "v1", "Pod", "ns", "pod-0"); got.ActiveTab != "pretty" {
		t.Fatalf("oldest state should be evicted to default, got %+v", got)
	}
}
