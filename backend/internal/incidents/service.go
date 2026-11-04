package incidents

import (
	"context"
	"errors"
	"log"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/events"
)

const defaultFetchLimit = 5000

type Service struct {
	db       *db.DB
	listener *events.EventListener
	changes  ChangeSource
}

func NewService(database *db.DB, listener *events.EventListener) *Service {
	return &Service{db: database, listener: listener}
}

func (s *Service) GetTimeline(ctx context.Context, f TimelineFilters) (TimelineResponse, error) {
	if f.Cluster == "" {
		return TimelineResponse{}, errors.New("cluster is required")
	}
	if s.listener != nil && !s.listener.IsListening(f.Cluster) {
		if err := s.listener.StartListening(f.Cluster); err != nil {
			log.Printf("incidents: failed to start event listener for %s: %v", f.Cluster, err)
		}
	}
	rows, err := s.db.GetClusterEvents(f.Cluster, defaultFetchLimit)
	if err != nil {
		return TimelineResponse{}, err
	}
	resp := BuildTimeline(rows, f)
	s.withChanges(ctx, f.Cluster, &resp)
	return resp, nil
}
