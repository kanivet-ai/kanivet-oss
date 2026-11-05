package featuretools

import (
	"context"
	"strings"

	"github.com/kanivet/backend/internal/cis"
	"github.com/kanivet/backend/internal/mcp"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
)

type cisArgs struct {
	mcpreg.ClusterArg
	Profile string `json:"profile,omitempty" jsonschema:"cis-k8s, cis-eks, cis-gke, cis-aks, k3s-hardening, rke2-hardening or openshift-policies; defaults to the one for the detected provider"`
	Status  string `json:"status,omitempty" jsonschema:"only controls with this status: automated-fail, automated-pass, manual, not-visible, provider-responsibility, error"`
	Section string `json:"section,omitempty" jsonschema:"only controls whose id starts with this, e.g. 5.1 or 4.2"`
}

type providerArgs struct {
	mcpreg.ClusterArg
}

// cisReasons turns RBAC degradations into user_forbidden reasons so an
// agent knows a not-visible control is about access, not absence.
func cisReasons(rep cis.Report) []mcp.Reason {
	var out []mcp.Reason
	for _, n := range rep.Notes {
		code := mcp.ReasonReadError
		if n.Kind == "rbac" {
			code = mcp.ReasonUserForbidden
		} else if n.Kind != "scanner" {
			continue
		}
		out = append(out, mcp.Reason{Code: code, Detail: n.Text})
	}
	return out
}

// FilterControls narrows a report's controls for the agent.
func FilterControls(cs []cis.ControlResult, status, section string) []cis.ControlResult {
	out := []cis.ControlResult{}
	for _, c := range cs {
		if status != "" && string(c.Status) != status {
			continue
		}
		if section != "" && c.ID != section && !strings.HasPrefix(c.ID, section+".") {
			continue
		}
		out = append(out, c)
	}
	return out
}

func registerCIS(r *mcpreg.Registry, svc *cis.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "cis_benchmark", Title: "CIS benchmark", Feature: "health",
		Description: "Read-only CIS Kubernetes Benchmark evaluation for the cluster's provider (generic/kubeadm, EKS, GKE, AKS, k3s, RKE2, OpenShift policies). Returns the score over automated controls, counts per status and each control with id, status (automated-pass, automated-fail, manual, provider-responsibility, not-visible, error), evidence (failing resources, kubelet node-pool values, control-plane flags), our remediation note and any Trivy Operator / Kubescape / kube-bench verdict. Filter with status and section." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in cisArgs) (any, error) {
		rep, err := svc.Benchmark(ctx, cis.Options{Cluster: call.Cluster, Profile: in.Profile})
		if err != nil {
			return explain(err, "", "")
		}
		rep.Controls = FilterControls(rep.Controls, in.Status, in.Section)
		rep.Profiles = nil
		return respond(rep, 150, nil, cisReasons(rep)...), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "detect_provider", Title: "Detect provider", Feature: "health",
		Description: "Which provider or distribution runs the cluster (EKS, GKE, AKS, OpenShift, k3s, RKE2, kind, minikube, Docker Desktop, kubeadm), its Kubernetes version, whether the control plane is managed or its static pods are visible, a 0..1 confidence and the evidence (node providerIDs and labels, server version, API host, API groups, static pods)." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, _ providerArgs) (any, error) {
		info, err := svc.DetectProvider(ctx, call.Cluster, false)
		if err != nil {
			return explain(err, "", "")
		}
		return respond(info, listCap, nil), nil
	})
}
