package certs

import (
	"context"
	"fmt"

	"github.com/kanivet/backend/internal/health"
)

// RegisterHealthSource feeds expired and expiring certificates into the
// health issues engine (rule "certs.<type>"), so they show on the Issues
// page, in the inspector health section and in get_issues.
func RegisterHealthSource(s *Service) {
	health.RegisterSignalSource("certs", func(ctx context.Context, cluster string) ([]health.Signal, error) {
		issues, err := s.ExpiringIssues(ctx, cluster)
		if err != nil {
			return nil, err
		}
		return HealthSignals(issues), nil
	})
}

// HealthSignals converts certificate issues to health signals.
func HealthSignals(issues []Issue) []health.Signal {
	out := make([]health.Signal, 0, len(issues))
	for _, is := range issues {
		sev := health.SeverityWarning
		if is.Severity == "critical" {
			sev = health.SeverityCritical
		}
		ref := health.ResourceRef{APIVersion: is.Resource.APIVersion, Kind: is.Resource.Kind, Namespace: is.Resource.Namespace, Name: is.Resource.Name}
		title := "Certificate expiring"
		hint := "Renew the certificate (or check cert-manager's Certificate status) before it expires."
		if is.Type == IssueExpired {
			title = "Certificate expired"
			hint = "Renew the certificate now; clients reject it."
		} else if is.Type == IssueWebhookExpiring {
			title = "Webhook CA expiring"
		}
		out = append(out, health.Signal{
			Rule:     fmt.Sprintf("certs.%s", is.Type),
			Severity: sev,
			Title:    title,
			Reason:   string(is.Type),
			Message:  is.Message,
			Hint:     hint,
			Object:   ref,
			Group:    ref,
		})
	}
	return out
}
