package changes

import (
	"context"
	"time"

	"github.com/kanivet/backend/internal/incidents"
)

// IncidentSource adapts the service to incidents.ChangeSource.
func (s *Service) IncidentSource() incidents.ChangeSource { return incidentSource{s} }

type incidentSource struct{ s *Service }

func (a incidentSource) ChangesSince(ctx context.Context, cluster string, since time.Time) ([]incidents.ChangeMarker, error) {
	feed, err := a.s.RecentChanges(ctx, cluster, Filter{Since: since, Limit: 2000})
	if err != nil {
		return nil, err
	}
	out := make([]incidents.ChangeMarker, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		cats := make([]string, len(e.Categories))
		for i, c := range e.Categories {
			cats[i] = string(c)
		}
		out = append(out, incidents.ChangeMarker{
			ID: e.ID, Source: string(e.Source), Time: e.Time,
			Group: e.Subject.Group, Version: e.Subject.Version, Kind: e.Subject.Kind,
			Namespace: e.Subject.Namespace, Name: e.Subject.Name,
			Title: e.Title, Summary: e.Summary, Categories: cats,
		})
	}
	return out, nil
}
