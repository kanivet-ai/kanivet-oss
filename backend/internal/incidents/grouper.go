package incidents

import (
	"sort"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/db"
)

type groupKey struct {
	kind, namespace, name, reason string
	severity                      Severity
}

func BuildTimeline(events []db.K8sEvent, f TimelineFilters) TimelineResponse {
	groups := make(map[groupKey]*TimelineEntry)
	var summary TimelineSummary

	for _, ev := range events {
		sev, routine := Classify(ev)
		summary.Total++
		if routine {
			summary.Routine++
		}
		switch sev {
		case SeverityCritical:
			summary.Critical++
		case SeverityWarning:
			summary.Warning++
		case SeverityInfo:
			if !routine {
				summary.Info++
			}
		}

		if !f.IncludeRoutine && routine {
			continue
		}
		if !matchFilters(ev, sev, f) {
			continue
		}

		key := groupKey{
			kind:      ev.InvolvedObjectKind,
			namespace: ev.InvolvedObjectNamespace,
			name:      ev.InvolvedObjectName,
			reason:    ev.Reason,
			severity:  sev,
		}
		entry, ok := groups[key]
		if !ok {
			entry = &TimelineEntry{
				ID:              entryID(ev, sev),
				Severity:        sev,
				Reason:          ev.Reason,
				Message:         ev.Message,
				Kind:            ev.InvolvedObjectKind,
				APIVersion:      ev.InvolvedObjectAPIVersion,
				Namespace:       ev.InvolvedObjectNamespace,
				Name:            ev.InvolvedObjectName,
				FirstTimestamp:  eventTime(ev),
				LastTimestamp:   eventTime(ev),
				SourceComponent: ev.SourceComponent,
				Routine:         routine,
			}
			groups[key] = entry
		}
		c := ev.Count
		if c < 1 {
			c = 1
		}
		entry.Count += c
		if ev.ID != 0 {
			entry.EventIDs = append(entry.EventIDs, ev.ID)
		}
		t := eventTime(ev)
		if t.After(entry.LastTimestamp) {
			entry.LastTimestamp = t
		}
		if t.Before(entry.FirstTimestamp) {
			entry.FirstTimestamp = t
		}
		if len(ev.Message) > len(entry.Message) {
			entry.Message = ev.Message
		}
	}

	out := make([]TimelineEntry, 0, len(groups))
	for _, e := range groups {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTimestamp.Equal(out[j].LastTimestamp) {
			return severityRank(out[i].Severity) > severityRank(out[j].Severity)
		}
		return out[i].LastTimestamp.After(out[j].LastTimestamp)
	})

	limit := f.Limit
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return TimelineResponse{Entries: out, Summary: summary}
}

func matchFilters(ev db.K8sEvent, sev Severity, f TimelineFilters) bool {
	if !f.Since.IsZero() && eventTime(ev).Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && eventTime(ev).After(f.Until) {
		return false
	}
	if len(f.Namespaces) > 0 && !contains(f.Namespaces, ev.InvolvedObjectNamespace) {
		return false
	}
	if len(f.Kinds) > 0 && !contains(f.Kinds, ev.InvolvedObjectKind) {
		return false
	}
	if len(f.Severities) > 0 {
		ok := false
		for _, s := range f.Severities {
			if s == sev {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.Search != "" {
		q := strings.ToLower(f.Search)
		if !strings.Contains(strings.ToLower(ev.Message), q) &&
			!strings.Contains(strings.ToLower(ev.InvolvedObjectName), q) &&
			!strings.Contains(strings.ToLower(ev.Reason), q) &&
			!strings.Contains(strings.ToLower(ev.InvolvedObjectKind), q) {
			return false
		}
	}
	return true
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func eventTime(ev db.K8sEvent) time.Time {
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime
	}
	return ev.CreatedAt
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

func entryID(ev db.K8sEvent, sev Severity) string {
	return string(sev) + ":" + ev.InvolvedObjectKind + ":" + ev.InvolvedObjectNamespace + ":" + ev.InvolvedObjectName + ":" + ev.Reason
}
