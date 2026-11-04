package incidents

import (
	"testing"
	"time"
)

func TestCorrelateChanges(t *testing.T) {
	now := time.Now()
	entries := []TimelineEntry{
		{ID: "a", Severity: SeverityCritical, Namespace: "prod", Name: "web-7d9f-abcde", FirstTimestamp: now, LastTimestamp: now.Add(time.Minute)},
		{ID: "b", Severity: SeverityInfo, Namespace: "prod", Name: "web-x", FirstTimestamp: now, LastTimestamp: now},
	}
	changes := []ChangeMarker{
		{ID: "rollout", Namespace: "prod", Name: "web", Time: now.Add(-10 * time.Minute)},
		{ID: "too-old", Namespace: "prod", Name: "web", Time: now.Add(-3 * time.Hour)},
		{ID: "other-ns", Namespace: "dev", Name: "web", Time: now.Add(-time.Minute)},
		{ID: "unrelated", Namespace: "prod", Name: "api", Time: now.Add(-time.Minute)},
		{ID: "after", Namespace: "prod", Name: "web", Time: now.Add(time.Hour)},
	}
	kept := correlateChanges(entries, changes)
	if len(kept) != 1 || kept[0].ID != "rollout" {
		t.Fatalf("kept %+v", kept)
	}
	if len(entries[0].RelatedChanges) != 1 || entries[1].RelatedChanges != nil {
		t.Fatalf("related %+v", entries)
	}
}
