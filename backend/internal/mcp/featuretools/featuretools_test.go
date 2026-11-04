package featuretools

import (
	"context"
	"errors"
	"net/http"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kanivet/backend/internal/certs"
	"github.com/kanivet/backend/internal/changes"
	"github.com/kanivet/backend/internal/compare"
	"github.com/kanivet/backend/internal/health"
	"github.com/kanivet/backend/internal/images"
	"github.com/kanivet/backend/internal/mcp"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
	"github.com/kanivet/backend/internal/network"
	"github.com/kanivet/backend/internal/rbac"
	"github.com/kanivet/backend/internal/topology"
	"github.com/kanivet/backend/internal/upgrade"
)

func allDeps() Deps {
	return Deps{
		Health: &health.Service{}, Changes: &changes.Service{}, Topology: &topology.Service{},
		Certs: &certs.Service{}, RBAC: &rbac.Service{}, Upgrade: &upgrade.Service{},
		Network: &network.Service{}, Compare: &compare.Service{}, Images: &images.Service{},
		Router: http.NotFoundHandler(),
	}
}

var readTools = []string{
	"get_issues", "audit", "get_changes", "get_change_snapshot", "get_topology", "get_related",
	"list_certificates", "who_can", "subject_permissions", "risky_grants", "upgrade_readiness",
	"check_network_path", "network_policy_coverage", "compare_resources", "get_drift",
	"gitops_status", "cnpg_cluster_status", "inspect_image",
}

var writeTools = []string{"network_probe", "flux_reconcile"}

func listTools(t *testing.T, s *mcpsdk.Server) map[string]*mcpsdk.Tool {
	t.Helper()
	ct, st := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcpsdk.Tool{}
	for _, tl := range res.Tools {
		out[tl.Name] = tl
	}
	return out
}

func TestFeatureToolsRegistered(t *testing.T) {
	r := mcpreg.New()
	RegisterOn(r, allDeps())
	impl := &mcpsdk.Implementation{Name: "k"}
	ro := listTools(t, r.NewServer(impl, nil, false))
	full := listTools(t, r.NewServer(impl, nil, true))
	for _, n := range readTools {
		if ro[n] == nil || full[n] == nil {
			t.Errorf("read tool %s missing (ro=%v full=%v)", n, ro[n] != nil, full[n] != nil)
		}
	}
	for _, n := range writeTools {
		if ro[n] != nil {
			t.Errorf("write tool %s exposed on /mcp-readonly", n)
		}
		if full[n] == nil {
			t.Errorf("write tool %s missing on /mcp", n)
		}
	}
	if d := full["network_probe"]; d == nil || d.Annotations.DestructiveHint == nil || !*d.Annotations.DestructiveHint {
		t.Error("network_probe must be destructive")
	}
}

func TestNilServicesSkipTools(t *testing.T) {
	r := mcpreg.New()
	RegisterOn(r, Deps{Certs: &certs.Service{}})
	if specs := r.Specs(); len(specs) != 1 || specs[0].Name != "list_certificates" {
		t.Fatalf("specs = %+v", specs)
	}
}

func TestExplainClassifiesErrors(t *testing.T) {
	gr := schema.GroupResource{Resource: "pods"}
	cases := map[error]string{
		apierrors.NewForbidden(gr, "x", errors.New("no")): mcp.ReasonUserForbidden,
		apierrors.NewNotFound(gr, "x"):                     mcp.ReasonObjectNotFound,
		apierrors.NewUnauthorized("expired"):              mcp.ReasonCredentialsRejected,
		errors.New("dial tcp: connection refused"):        mcp.ReasonClusterDisconnected,
		errors.New("boom"):                                mcp.ReasonReadError,
	}
	for err, want := range cases {
		out, _ := explain(err, "Pod", "ns")
		w := out.(mcp.WithReasons)
		if len(w.Reasons) != 1 || w.Reasons[0].Code != want {
			t.Errorf("%v: got %+v, want %s", err, w.Reasons, want)
		}
	}
}

func TestRespondTruncatesAndScrubs(t *testing.T) {
	items := make([]string, 5)
	for i := range items {
		items[i] = "x"
	}
	items[0] = "password=hunter2"
	out := respond(map[string]any{"items": items, "warnings": []string{"secrets: forbidden"}}, 3, []string{"secrets: forbidden"}).(map[string]any)
	if got := len(out["items"].([]any)); got != 3 {
		t.Fatalf("items not cut: %d", got)
	}
	if out["items"].([]any)[0] == "password=hunter2" {
		t.Fatal("secret not scrubbed")
	}
	rs := out["reasons"].([]mcp.Reason)
	codes := map[string]bool{}
	for _, r := range rs {
		codes[r.Code] = true
	}
	if !codes[mcp.ReasonTruncated] || !codes[mcp.ReasonUserForbidden] {
		t.Fatalf("reasons = %+v", rs)
	}
}

func TestRedactCompareSecret(t *testing.T) {
	res := redactCompare("Secret", &compare.Result{Changes: []compare.FieldChange{{Path: "data.password", A: "YQ==", B: "Yg=="}, {Path: "metadata.labels.app", A: "a", B: "b"}}})
	if res.Changes[0].A != "<redacted>" || res.Changes[1].A != "a" {
		t.Fatalf("%+v", res.Changes)
	}
}
