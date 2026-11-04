package gitops

import (
	"context"
	"sort"

	"github.com/kanivet/backend/internal/argo"
	"k8s.io/client-go/dynamic"
)

// Unified health values shared by Argo and Flux rows.
const (
	HealthHealthy     = "Healthy"
	HealthFailing     = "Failing"
	HealthProgressing = "Progressing"
	HealthSuspended   = "Suspended"
	HealthUnknown     = "Unknown"
)

// Item is one row of the unified GitOps view: an Argo Application or a Flux
// Kustomization/HelmRelease/source, normalised to the same columns.
type Item struct {
	Tool       string `json:"tool"` // "argocd" or "flux"
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	// Health is Healthy, Failing, Progressing, Suspended or Unknown.
	Health string `json:"health"`
	// Detail is the tool's own status words ("OutOfSync · Degraded",
	// "ReconciliationFailed").
	Detail    string `json:"detail,omitempty"`
	Message   string `json:"message,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Source    string `json:"source,omitempty"`
	Suspended bool   `json:"suspended"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// Status is the GitOpsStatus answer.
type Status struct {
	ArgoInstalled bool           `json:"argoInstalled"`
	FluxInstalled bool           `json:"fluxInstalled"`
	Items         []Item         `json:"items"`
	ByHealth      map[string]int `json:"byHealth"`
	Flux          FluxSummary    `json:"flux"`
	ArgoTotal     int            `json:"argoTotal"`
	Errors        []string       `json:"errors,omitempty"`
}

// FluxHealth maps a Flux status to the unified health.
func FluxHealth(status string) string {
	switch status {
	case StatusReady:
		return HealthHealthy
	case StatusFailed:
		return HealthFailing
	case StatusProgressing:
		return HealthProgressing
	case StatusSuspended:
		return HealthSuspended
	}
	return HealthUnknown
}

// ArgoHealth maps Argo sync+health to the unified health.
func ArgoHealth(sync, health string) string {
	switch health {
	case "Degraded", "Missing":
		return HealthFailing
	case "Progressing":
		return HealthProgressing
	case "Suspended":
		return HealthSuspended
	case "Healthy":
		if sync == "OutOfSync" {
			return HealthProgressing
		}
		return HealthHealthy
	}
	return HealthUnknown
}

// FromFlux converts a Flux object to a unified row.
func FromFlux(o FluxObject) Item {
	src := o.URL
	if o.SourceRef != nil {
		src = o.SourceRef.Kind + "/" + o.SourceRef.Name
	}
	return Item{
		Tool: "flux", Kind: o.Kind, APIVersion: o.APIVersion, Namespace: o.Namespace, Name: o.Name,
		Health: FluxHealth(o.Status), Detail: o.Reason, Message: o.Message, Revision: o.Revision,
		Source: src, Suspended: o.Suspended, UpdatedAt: o.LastReconciledAt,
	}
}

// FromArgo converts an Argo application to a unified row.
func FromArgo(a argo.AppListEntry) Item {
	detail := a.SyncStatus
	if a.Health != "" {
		if detail != "" {
			detail += " · "
		}
		detail += a.Health
	}
	return Item{
		Tool: "argocd", Kind: "Application", APIVersion: "argoproj.io/v1alpha1", Namespace: a.Namespace, Name: a.Name,
		Health: ArgoHealth(a.SyncStatus, a.Health), Detail: detail, Revision: a.Revision, Source: a.RepoURL,
		UpdatedAt: a.LastSyncedAt,
	}
}

// BuildStatus merges both tools' rows. Failing rows sort first.
func BuildStatus(argoInstalled bool, apps []argo.AppListEntry, fluxInstalled bool, flux []FluxObject) *Status {
	st := &Status{ArgoInstalled: argoInstalled, FluxInstalled: fluxInstalled, Items: []Item{}, ByHealth: map[string]int{}, Flux: SummarizeFlux(flux), ArgoTotal: len(apps)}
	for _, a := range apps {
		st.Items = append(st.Items, FromArgo(a))
	}
	for _, f := range flux {
		st.Items = append(st.Items, FromFlux(f))
	}
	rank := map[string]int{HealthFailing: 0, HealthProgressing: 1, HealthSuspended: 2, HealthUnknown: 3, HealthHealthy: 4}
	sort.SliceStable(st.Items, func(i, j int) bool {
		a, b := st.Items[i], st.Items[j]
		if rank[a.Health] != rank[b.Health] {
			return rank[a.Health] < rank[b.Health]
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	for _, it := range st.Items {
		st.ByHealth[it.Health]++
	}
	return st
}

// GitOpsStatus reads Argo applications (when argoInstalled) and every Flux
// object, and merges them. Partial failures are reported in Errors.
func GitOpsStatus(ctx context.Context, dyn dynamic.Interface, argoInstalled bool, fluxDet *FluxDetection) (*Status, error) {
	var apps []argo.AppListEntry
	var errs []string
	if argoInstalled {
		a, err := argo.ListApplicationsSummary(ctx, dyn)
		if err != nil {
			errs = append(errs, "argo: "+err.Error())
		}
		apps = a
	}
	var flux []FluxObject
	fluxInstalled := fluxDet != nil && fluxDet.Installed
	if fluxInstalled {
		f, err := ListFlux(ctx, dyn, fluxDet)
		if err != nil {
			errs = append(errs, "flux: "+err.Error())
		}
		flux = f
	}
	st := BuildStatus(argoInstalled, apps, fluxInstalled, flux)
	st.Errors = errs
	return st, nil
}
