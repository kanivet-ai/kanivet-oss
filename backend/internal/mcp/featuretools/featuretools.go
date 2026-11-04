// Package featuretools registers the MCP tools of Kanivet's feature
// packages (health, changes, topology, certs, rbac, upgrade, network,
// compare, images, GitOps/CNPG integrations) on the mcpreg registry.
//
// It lives apart from internal/mcp so that mcp never imports the features
// (and the features never import mcp): main builds the services, then calls
// Register once with whichever of them exist. Nil services skip their tools.
package featuretools

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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

// Deps are the feature services. Any may be nil.
type Deps struct {
	Health   *health.Service
	Changes  *changes.Service
	Topology *topology.Service
	Certs    *certs.Service
	RBAC     *rbac.Service
	Upgrade  *upgrade.Service
	Network  *network.Service
	Compare  *compare.Service
	Images   *images.Service
	// Router serves the integration endpoints (GitOps, CNPG, Flux) in-process.
	Router http.Handler
}

// Register adds the feature tools to mcpreg.Default and hooks health issues
// and recent changes into the core diagnose tool.
func Register(d Deps) { RegisterOn(mcpreg.Default, d) }

// RegisterOn adds the tools to r (tests use a fresh registry).
func RegisterOn(r *mcpreg.Registry, d Deps) {
	if d.Health != nil {
		registerHealth(r, d.Health)
	}
	if d.Changes != nil {
		registerChanges(r, d.Changes)
	}
	if d.Topology != nil {
		registerTopology(r, d.Topology)
	}
	if d.Certs != nil {
		registerCerts(r, d.Certs)
	}
	if d.RBAC != nil {
		registerRBAC(r, d.RBAC)
	}
	if d.Upgrade != nil {
		registerUpgrade(r, d.Upgrade)
	}
	if d.Network != nil {
		registerNetwork(r, d.Network)
	}
	if d.Compare != nil {
		registerCompare(r, d.Compare)
	}
	if d.Images != nil {
		registerImages(r, d.Images)
	}
	if d.Router != nil {
		registerIntegrations(r, d.Router)
	}
	registerDiagnose(d)
}

// ---------------------------------------------------------------------------
// shared helpers

// defaultAPIVersion covers the kinds agents name most; anything else needs
// an explicit apiVersion.
var defaultAPIVersion = map[string]string{
	"Pod": "v1", "Service": "v1", "ConfigMap": "v1", "Secret": "v1", "ServiceAccount": "v1",
	"PersistentVolumeClaim": "v1", "PersistentVolume": "v1", "Node": "v1", "Namespace": "v1", "Endpoints": "v1",
	"Deployment": "apps/v1", "StatefulSet": "apps/v1", "DaemonSet": "apps/v1", "ReplicaSet": "apps/v1",
	"Job": "batch/v1", "CronJob": "batch/v1",
	"Ingress": "networking.k8s.io/v1", "NetworkPolicy": "networking.k8s.io/v1",
	"HorizontalPodAutoscaler": "autoscaling/v2",
	"Role":                    "rbac.authorization.k8s.io/v1", "ClusterRole": "rbac.authorization.k8s.io/v1",
	"RoleBinding": "rbac.authorization.k8s.io/v1", "ClusterRoleBinding": "rbac.authorization.k8s.io/v1",
}

// groupVersion splits apiVersion (or the kind's default) into group/version.
func groupVersion(kind, apiVersion string) (string, string) {
	if apiVersion == "" {
		apiVersion = defaultAPIVersion[kind]
	}
	if apiVersion == "" {
		apiVersion = "v1"
	}
	if i := strings.LastIndex(apiVersion, "/"); i >= 0 {
		return apiVersion[:i], apiVersion[i+1:]
	}
	return "", apiVersion
}

// objectArgs names one object.
type objectArgs struct {
	mcpreg.ClusterArg
	Kind       string `json:"kind" jsonschema:"object kind, e.g. Deployment"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"namespace (omit for cluster-scoped kinds)"`
	Name       string `json:"name" jsonschema:"object name"`
	APIVersion string `json:"apiVersion,omitempty" jsonschema:"e.g. apps/v1; defaults for built-in kinds"`
}

// shape turns v into plain JSON values, scrubs every string of secrets, and
// cuts arrays longer than limit (recording a truncated reason for each).
func shape(v any, limit int) (any, []mcp.Reason) {
	b, err := json.Marshal(v)
	if err != nil {
		return v, nil
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v, nil
	}
	var reasons []mcp.Reason
	var walk func(x any, path string) any
	walk = func(x any, path string) any {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				t[k] = walk(val, joinPath(path, k))
			}
			return t
		case []any:
			if limit > 0 && len(t) > limit {
				reasons = append(reasons, mcp.Reason{Code: mcp.ReasonTruncated, Detail: path + ": showing " + itoa(limit) + " of " + itoa(len(t)) + "; narrow the query"})
				t = t[:limit]
			}
			for i := range t {
				t[i] = walk(t[i], path)
			}
			return t
		case string:
			return mcp.ScrubText(t)
		}
		return x
	}
	out = walk(out, "")
	return out, reasons
}

func joinPath(p, k string) string {
	if p == "" {
		return k
	}
	return p + "." + k
}

func itoa(n int) string { return strconv.Itoa(n) }

// respond shapes v and attaches reasons (from warnings, truncation and the
// caller). An empty reasons list is omitted.
func respond(v any, limit int, warnings []string, extra ...mcp.Reason) any {
	out, cut := shape(v, limit)
	reasons := append(mcp.ReasonsFromWarnings(warnings), cut...)
	reasons = append(reasons, extra...)
	m, ok := out.(map[string]any)
	if !ok {
		if len(reasons) == 0 {
			return out
		}
		return map[string]any{"items": out, "reasons": reasons}
	}
	delete(m, "warnings")
	if len(reasons) > 0 {
		m["reasons"] = reasons
	}
	return m
}

// explain turns an error into an empty result with a reason an agent can act
// on. Errors that are the caller's fault (bad arguments) stay errors.
func explain(err error, kind, namespace string) (any, error) {
	return mcp.Explain(err, kind, namespace), nil
}

const reasonsDoc = mcp.ReasonsDoc
