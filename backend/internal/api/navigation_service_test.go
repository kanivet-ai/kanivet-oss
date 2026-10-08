package api

import (
	"encoding/json"
	"testing"

	"github.com/kanivet/backend/internal/models"
)

func TestNavigationAcrossClusters(t *testing.T) {
	s := NewNavigationService()
	s.AddEntry("workspace", "cluster-a", models.NavigationEntry{Type: "item", Path: "ns/pod-a", PaneID: "right"})
	s.AddEntry("workspace", "cluster-b", models.NavigationEntry{Type: "overview", Path: "cluster-overview"})
	previous := s.NavigateBack("workspace")
	if previous == nil || previous.ClusterID != "cluster-a" || previous.Path != "ns/pod-a" || previous.PaneID != "right" {
		t.Fatalf("back = %+v", previous)
	}
	encoded, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	var decoded models.NavigationEntry
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ClusterID != "cluster-a" || decoded.PaneID != "right" {
		t.Fatalf("round trip lost destination: %+v", decoded)
	}
	next := s.NavigateForward("workspace")
	if next == nil || next.ClusterID != "cluster-b" {
		t.Fatalf("forward = %+v", next)
	}
	s.NavigateBack("workspace")
	s.AddEntry("workspace", "cluster-c", models.NavigationEntry{Type: "resource", Path: "pods"})
	if s.NavigateForward("workspace") != nil {
		t.Fatal("new visit did not discard forward history")
	}
}
