package incidents

import (
	"fmt"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/db"
)

func mkEvent(id uint, kind, ns, name, reason, typ, msg string, count int32, ts time.Time) db.K8sEvent {
	return db.K8sEvent{
		ID:                       id,
		Cluster:                  "c1",
		UID:                      fmt.Sprintf("u%d", id),
		InvolvedObjectKind:       kind,
		InvolvedObjectNamespace:  ns,
		InvolvedObjectName:       name,
		InvolvedObjectAPIVersion: "v1",
		Type:                     typ,
		Reason:                   reason,
		Message:                  msg,
		Count:                    count,
		FirstTimestamp:           ts,
		LastTimestamp:            ts,
		EventTime:                ts,
	}
}

func TestBuildTimeline_GroupsSameResourceAndReason(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "web-0", "BackOff", "Warning", "Back-off restarting", 3, base),
		mkEvent(2, "Pod", "default", "web-0", "BackOff", "Warning", "Back-off restarting", 2, base.Add(30*time.Second)),
		mkEvent(3, "Pod", "default", "web-0", "Unhealthy", "Warning", "Readiness probe failed", 1, base),
	}
	resp := BuildTimeline(events, TimelineFilters{IncludeRoutine: true})
	if len(resp.Entries) != 2 {
		t.Fatalf("entries: got %d, want 2", len(resp.Entries))
	}
	var backoff *TimelineEntry
	for i := range resp.Entries {
		if resp.Entries[i].Reason == "BackOff" {
			backoff = &resp.Entries[i]
		}
	}
	if backoff == nil {
		t.Fatal("missing BackOff entry")
	}
	if backoff.Count != 5 {
		t.Fatalf("count: got %d, want 5", backoff.Count)
	}
	if len(backoff.EventIDs) != 2 {
		t.Fatalf("eventIds: got %d, want 2", len(backoff.EventIDs))
	}
	if backoff.Severity != SeverityCritical {
		t.Fatalf("severity: got %q", backoff.Severity)
	}
}

func TestBuildTimeline_FiltersRoutineByDefault(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "web-0", "Pulled", "Normal", "pulled", 1, base),
		mkEvent(2, "Pod", "default", "web-0", "BackOff", "Warning", "back-off", 1, base),
	}
	resp := BuildTimeline(events, TimelineFilters{IncludeRoutine: false})
	if len(resp.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(resp.Entries))
	}
	if resp.Entries[0].Reason != "BackOff" {
		t.Fatalf("reason: got %q", resp.Entries[0].Reason)
	}
	if resp.Summary.Routine != 1 {
		t.Fatalf("summary.routine: got %d, want 1", resp.Summary.Routine)
	}
}

func TestBuildTimeline_AppliesFilters(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "web-0", "BackOff", "Warning", "a", 1, base),
		mkEvent(2, "Pod", "kube-system", "coredns", "Unhealthy", "Warning", "b", 1, base),
		mkEvent(3, "Deployment", "default", "web", "ScalingReplicaSet", "Normal", "scale", 1, base),
	}
	resp := BuildTimeline(events, TimelineFilters{
		Namespaces:     []string{"default"},
		Kinds:          []string{"Pod"},
		Severities:     []Severity{SeverityCritical},
		IncludeRoutine: true,
	})
	if len(resp.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(resp.Entries))
	}
	if resp.Entries[0].Name != "web-0" {
		t.Fatalf("name: got %q", resp.Entries[0].Name)
	}
}

func TestBuildTimeline_SearchMatchesNameMessageReason(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "web-0", "BackOff", "Warning", "back-off", 1, base),
		mkEvent(2, "Pod", "default", "api-1", "BackOff", "Warning", "back-off", 1, base),
	}
	resp := BuildTimeline(events, TimelineFilters{Search: "api", IncludeRoutine: true})
	if len(resp.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(resp.Entries))
	}
	if resp.Entries[0].Name != "api-1" {
		t.Fatalf("name: got %q", resp.Entries[0].Name)
	}
}

func TestBuildTimeline_SortsByLastTimestampDesc(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "old", "BackOff", "Warning", "a", 1, base),
		mkEvent(2, "Pod", "default", "new", "BackOff", "Warning", "b", 1, base.Add(time.Hour)),
	}
	resp := BuildTimeline(events, TimelineFilters{IncludeRoutine: true})
	if len(resp.Entries) != 2 {
		t.Fatalf("entries: got %d", len(resp.Entries))
	}
	if resp.Entries[0].Name != "new" {
		t.Fatalf("first: got %q, want new", resp.Entries[0].Name)
	}
}

func TestBuildTimeline_SummaryCounts(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "web-0", "BackOff", "Warning", "a", 1, base),
		mkEvent(2, "Pod", "default", "web-0", "Unhealthy", "Warning", "b", 1, base),
		mkEvent(3, "Pod", "default", "web-1", "Pulled", "Normal", "c", 1, base),
		mkEvent(4, "Pod", "default", "web-2", "Started", "Normal", "d", 1, base),
	}
	resp := BuildTimeline(events, TimelineFilters{IncludeRoutine: true})
	if resp.Summary.Critical != 1 || resp.Summary.Warning != 1 || resp.Summary.Routine != 2 {
		t.Fatalf("summary: %+v", resp.Summary)
	}
	if resp.Summary.Total != 4 {
		t.Fatalf("total: %d", resp.Summary.Total)
	}
}

func TestBuildTimeline_LimitCapsResults(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "a", "BackOff", "Warning", "", 1, base),
		mkEvent(2, "Pod", "default", "b", "BackOff", "Warning", "", 1, base.Add(time.Second)),
		mkEvent(3, "Pod", "default", "c", "BackOff", "Warning", "", 1, base.Add(2*time.Second)),
	}
	resp := BuildTimeline(events, TimelineFilters{IncludeRoutine: true, Limit: 2})
	if len(resp.Entries) != 2 {
		t.Fatalf("entries: got %d, want 2", len(resp.Entries))
	}
}

func TestBuildTimeline_PrefersLongestMessage(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "web-0", "Failed", "Warning",
			"Failed to pull image \"x:1\": rpc error: code = NotFound desc = not found", 1, base),
		mkEvent(2, "Pod", "default", "web-0", "Failed", "Warning",
			"Error: ImagePullBackOff", 1, base.Add(time.Minute)),
	}
	resp := BuildTimeline(events, TimelineFilters{IncludeRoutine: true})
	if len(resp.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(resp.Entries))
	}
	got := resp.Entries[0].Message
	if got != "Failed to pull image \"x:1\": rpc error: code = NotFound desc = not found" {
		t.Fatalf("message: got %q, want the longer/richer one", got)
	}
}

func TestBuildTimeline_TimeWindowFilter(t *testing.T) {
	base := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	events := []db.K8sEvent{
		mkEvent(1, "Pod", "default", "old", "BackOff", "Warning", "", 1, base.Add(-2*time.Hour)),
		mkEvent(2, "Pod", "default", "new", "BackOff", "Warning", "", 1, base),
	}
	resp := BuildTimeline(events, TimelineFilters{Since: base.Add(-time.Hour), IncludeRoutine: true})
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "new" {
		t.Fatalf("window filter failed: %+v", resp.Entries)
	}
}
