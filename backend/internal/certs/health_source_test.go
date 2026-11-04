package certs

import (
	"testing"

	"github.com/kanivet/backend/internal/health"
)

func TestHealthSignals(t *testing.T) {
	sigs := HealthSignals([]Issue{
		{Type: IssueExpired, Severity: "critical", Message: "expired 2d ago", Resource: Ref{Kind: "Secret", Namespace: "a", Name: "tls"}},
		{Type: IssueExpiring, Severity: "warning", Message: "expires in 5d", Resource: Ref{Kind: "Secret", Namespace: "a", Name: "tls2"}},
	})
	if len(sigs) != 2 || sigs[0].Severity != health.SeverityCritical || sigs[0].Rule != "certs.expired" || sigs[1].Severity != health.SeverityWarning {
		t.Fatalf("unexpected signals: %+v", sigs)
	}
	snap := &health.Snapshot{External: sigs}
	rep := health.EvaluateIssues(snap)
	if rep.Summary.Critical != 1 || rep.Summary.Warning != 1 {
		t.Fatalf("issues from external signals: %+v", rep.Summary)
	}
}
