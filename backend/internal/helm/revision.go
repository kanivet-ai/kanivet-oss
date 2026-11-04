package helm

import (
	"context"
	"fmt"

	"helm.sh/helm/v3/pkg/action"
)

// ReleaseRevision is one stored revision of a release: what it deployed and
// the user-supplied values it was rendered with.
type ReleaseRevision struct {
	Revision int            `json:"revision"`
	Manifest string         `json:"manifest"`
	Values   map[string]any `json:"values"`
}

// GetReleaseRevision reads one revision from the release's storage secrets.
func (s *Service) GetReleaseRevision(ctx context.Context, cluster, namespace, name string, revision int) (*ReleaseRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return nil, err
	}
	get := action.NewGet(actionConfig)
	get.Version = revision
	rel, err := get.Run(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get release revision %d: %w", revision, err)
	}
	return &ReleaseRevision{Revision: rel.Version, Manifest: rel.Manifest, Values: rel.Config}, nil
}
