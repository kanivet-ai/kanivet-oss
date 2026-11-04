package incidents

import (
	"context"
	"sort"
	"strings"
	"time"
)

// ChangeMarker is a change to a resource (a rollout, a Helm upgrade, a sync,
// an edit) shown in the timeline next to the events it may explain.
type ChangeMarker struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Time       time.Time `json:"time"`
	Group      string    `json:"group"`
	Version    string    `json:"version"`
	Kind       string    `json:"kind"`
	Namespace  string    `json:"namespace"`
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary,omitempty"`
	Categories []string  `json:"categories"`
}

// ChangeSource reports changes in a cluster since a time. It is implemented
// by the changes package; incidents works without one.
type ChangeSource interface {
	ChangesSince(ctx context.Context, cluster string, since time.Time) ([]ChangeMarker, error)
}

// SetChangeSource adds "what changed before it broke" to timelines.
func (s *Service) SetChangeSource(cs ChangeSource) { s.changes = cs }

// correlationWindow is how long before an incident's first event a change
// is still considered a likely cause.
const correlationWindow = 2 * time.Hour

// relatedName reports whether a change to changeName can explain an event on
// eventName: same object, or the event's object is named after it (pods and
// ReplicaSets of a Deployment, pods of a StatefulSet).
func relatedName(changeName, eventName string) bool {
	return changeName == eventName || strings.HasPrefix(eventName, changeName+"-")
}

// correlateChanges keeps the changes that precede a non-routine warning or
// critical entry in the same namespace and links each such entry to them,
// newest change first.
func correlateChanges(entries []TimelineEntry, changes []ChangeMarker) []ChangeMarker {
	used := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if e.Routine || e.Severity == SeverityInfo {
			continue
		}
		start := e.FirstTimestamp
		if start.IsZero() {
			start = e.LastTimestamp
		}
		var rel []ChangeMarker
		for _, c := range changes {
			if c.Namespace != e.Namespace && c.Namespace != "" {
				continue
			}
			if c.Time.After(e.LastTimestamp) || c.Time.Before(start.Add(-correlationWindow)) {
				continue
			}
			if c.Namespace == "" || relatedName(c.Name, e.Name) {
				rel = append(rel, c)
			}
		}
		sort.Slice(rel, func(a, b int) bool { return rel[a].Time.After(rel[b].Time) })
		for _, c := range rel {
			e.RelatedChanges = append(e.RelatedChanges, c.ID)
			used[c.ID] = true
		}
	}
	out := []ChangeMarker{}
	for _, c := range changes {
		if used[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// withChanges attaches correlated changes to a built timeline.
func (s *Service) withChanges(ctx context.Context, cluster string, resp *TimelineResponse) {
	if s.changes == nil || len(resp.Entries) == 0 {
		return
	}
	earliest := time.Now()
	for _, e := range resp.Entries {
		t := e.FirstTimestamp
		if t.IsZero() {
			t = e.LastTimestamp
		}
		if !t.IsZero() && t.Before(earliest) {
			earliest = t
		}
	}
	changes, err := s.changes.ChangesSince(ctx, cluster, earliest.Add(-correlationWindow))
	if err != nil {
		return
	}
	resp.Changes = correlateChanges(resp.Entries, changes)
}
