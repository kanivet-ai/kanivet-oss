package featuretools

import (
	"testing"

	"github.com/kanivet/backend/internal/cis"
	"github.com/kanivet/backend/internal/mcp"
)

func TestCISFilterAndReasons(t *testing.T) {
	cs := []cis.ControlResult{
		{ID: "5.1.1", Status: cis.StatusFail}, {ID: "5.1.10", Status: cis.StatusPass}, {ID: "5.10.1", Status: cis.StatusFail}, {ID: "4.2.1", Status: cis.StatusNotVisible},
	}
	if got := FilterControls(cs, "automated-fail", ""); len(got) != 2 {
		t.Fatalf("status %v", got)
	}
	got := FilterControls(cs, "", "5.1")
	if len(got) != 2 || got[0].ID != "5.1.1" || got[1].ID != "5.1.10" {
		t.Fatalf("section must match whole id parts: %v", got)
	}
	rs := cisReasons(cis.Report{Notes: []cis.Note{{Kind: "rbac", Text: "grant nodes/proxy"}, {Kind: "info", Text: "sampled"}, {Kind: "scanner", Text: "install trivy"}}})
	if len(rs) != 2 || rs[0].Code != mcp.ReasonUserForbidden || rs[1].Code != mcp.ReasonReadError {
		t.Fatalf("reasons %+v", rs)
	}
}
