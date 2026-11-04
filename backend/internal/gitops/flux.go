// Package gitops reads Flux CD objects, merges them with Argo CD applications
// into one GitOps view, and works out which GitOps object manages a resource.
// See README.md for the service API.
package gitops

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/crdprobe"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	GroupSource    = "source.toolkit.fluxcd.io"
	GroupKustomize = "kustomize.toolkit.fluxcd.io"
	GroupHelm      = "helm.toolkit.fluxcd.io"

	// ReconcileAnnotation asks a Flux controller to reconcile now. The value
	// must change on every request; Flux records the handled value in
	// status.lastHandledReconcileAt.
	ReconcileAnnotation = "reconcile.fluxcd.io/requestedAt"
)

// Category groups Flux kinds the way the UI shows them.
type Category string

const (
	CategorySource        Category = "source"
	CategoryKustomization Category = "kustomization"
	CategoryHelmRelease   Category = "helmrelease"
)

// FluxKind is one Flux CRD Kanivet understands.
type FluxKind struct {
	Kind     string   `json:"kind"`
	Plural   string   `json:"plural"`
	Group    string   `json:"group"`
	Version  string   `json:"version,omitempty"`
	Category Category `json:"category"`
}

// GVR of the kind at its resolved version.
func (k FluxKind) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: k.Group, Version: k.Version, Resource: k.Plural}
}

// APIVersion of the kind at its resolved version.
func (k FluxKind) APIVersion() string { return k.Group + "/" + k.Version }

// KnownFluxKinds lists every kind in reading order. Version is unset; Detect
// fills it from the CRD.
var KnownFluxKinds = []FluxKind{
	{Kind: "GitRepository", Plural: "gitrepositories", Group: GroupSource, Category: CategorySource},
	{Kind: "OCIRepository", Plural: "ocirepositories", Group: GroupSource, Category: CategorySource},
	{Kind: "HelmRepository", Plural: "helmrepositories", Group: GroupSource, Category: CategorySource},
	{Kind: "Bucket", Plural: "buckets", Group: GroupSource, Category: CategorySource},
	{Kind: "Kustomization", Plural: "kustomizations", Group: GroupKustomize, Category: CategoryKustomization},
	{Kind: "HelmRelease", Plural: "helmreleases", Group: GroupHelm, Category: CategoryHelmRelease},
}

// IsFluxGroup reports whether an API group belongs to Flux.
func IsFluxGroup(group string) bool { return strings.HasSuffix(group, ".toolkit.fluxcd.io") }

// FluxDetection says whether Flux is installed and which kinds are served.
type FluxDetection struct {
	Installed bool       `json:"installed"`
	Kinds     []FluxKind `json:"kinds"`
}

// Kind returns the detected kind by name (case-insensitive), or false.
func (d *FluxDetection) Kind(kind string) (FluxKind, bool) {
	if d == nil {
		return FluxKind{}, false
	}
	for _, k := range d.Kinds {
		if strings.EqualFold(k.Kind, kind) || strings.EqualFold(k.Plural, kind) {
			return k, true
		}
	}
	return FluxKind{}, false
}

// DetectFlux probes each Flux CRD. A cluster without any of them is not a
// Flux cluster; RBAC-hidden CRDs count as absent.
func DetectFlux(ctx context.Context, dyn dynamic.Interface) (*FluxDetection, error) {
	det := &FluxDetection{Kinds: []FluxKind{}}
	if dyn == nil {
		return det, nil
	}
	var firstErr error
	for _, k := range KnownFluxKinds {
		res, err := crdprobe.Probe(ctx, dyn, k.Plural, k.Group)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !res.Found {
			continue
		}
		k.Version = res.Version
		det.Kinds = append(det.Kinds, k)
	}
	det.Installed = len(det.Kinds) > 0
	if !det.Installed && firstErr != nil {
		return det, firstErr
	}
	return det, nil
}

// ObjectRef points at any Kubernetes object.
type ObjectRef struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

// Flux status values. They are derived, see deriveStatus.
const (
	StatusReady       = "Ready"
	StatusFailed      = "Failed"
	StatusProgressing = "Progressing"
	StatusSuspended   = "Suspended"
	StatusUnknown     = "Unknown"
)

// FluxObject is the list view of one Flux object.
type FluxObject struct {
	Kind       string   `json:"kind"`
	APIVersion string   `json:"apiVersion"`
	Category   Category `json:"category"`
	Namespace  string   `json:"namespace"`
	Name       string   `json:"name"`
	// Status is Ready, Failed, Progressing, Suspended or Unknown.
	Status  string `json:"status"`
	Ready   string `json:"ready"` // Ready condition status: True, False, Unknown or ""
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// Suspended mirrors spec.suspend.
	Suspended bool `json:"suspended"`
	// Revision is the last applied revision (Kustomization, HelmRelease) or
	// the stored artifact revision (sources).
	Revision          string     `json:"revision,omitempty"`
	AttemptedRevision string     `json:"attemptedRevision,omitempty"`
	URL               string     `json:"url,omitempty"`
	Path              string     `json:"path,omitempty"`
	SourceRef         *ObjectRef `json:"sourceRef,omitempty"`
	Chart             string     `json:"chart,omitempty"`
	ChartVersion      string     `json:"chartVersion,omitempty"`
	TargetNamespace   string     `json:"targetNamespace,omitempty"`
	Interval          string     `json:"interval,omitempty"`
	// LastReconciledAt is when the Ready condition last changed.
	LastReconciledAt     string `json:"lastReconciledAt,omitempty"`
	LastHandledReconcile string `json:"lastHandledReconcileAt,omitempty"`
	InventoryCount       int    `json:"inventoryCount"`
	CreatedAt            string `json:"createdAt,omitempty"`
}

// FluxObjectDetail is a FluxObject with conditions and the managed inventory.
type FluxObjectDetail struct {
	FluxObject
	Conditions []Condition   `json:"conditions"`
	Inventory  []ObjectRef   `json:"inventory"`
	History    []HelmHistory `json:"history,omitempty"`
}

// HelmHistory is one entry of a HelmRelease's status.history (Flux v2.1+).
type HelmHistory struct {
	Version       int64  `json:"version"`
	ChartName     string `json:"chartName,omitempty"`
	ChartVersion  string `json:"chartVersion,omitempty"`
	AppVersion    string `json:"appVersion,omitempty"`
	Status        string `json:"status,omitempty"`
	FirstDeployed string `json:"firstDeployed,omitempty"`
	LastDeployed  string `json:"lastDeployed,omitempty"`
}

// ParseFluxObject turns one unstructured Flux object into its list view.
func ParseFluxObject(k FluxKind, obj *unstructured.Unstructured) FluxObject {
	o := obj.Object
	out := FluxObject{
		Kind:                 k.Kind,
		APIVersion:           obj.GetAPIVersion(),
		Category:             k.Category,
		Namespace:            obj.GetNamespace(),
		Name:                 obj.GetName(),
		Interval:             nestedStr(o, "spec", "interval"),
		URL:                  nestedStr(o, "spec", "url"),
		Path:                 nestedStr(o, "spec", "path"),
		TargetNamespace:      nestedStr(o, "spec", "targetNamespace"),
		LastHandledReconcile: nestedStr(o, "status", "lastHandledReconcileAt"),
	}
	if out.APIVersion == "" && k.Version != "" {
		out.APIVersion = k.APIVersion()
	}
	if ts := obj.GetCreationTimestamp(); !ts.IsZero() {
		out.CreatedAt = ts.UTC().Format(time.RFC3339)
	}
	out.Suspended, _, _ = unstructured.NestedBool(o, "spec", "suspend")

	switch k.Category {
	case CategorySource:
		out.Revision = nestedStr(o, "status", "artifact", "revision")
	case CategoryKustomization:
		out.Revision = nestedStr(o, "status", "lastAppliedRevision")
		out.AttemptedRevision = nestedStr(o, "status", "lastAttemptedRevision")
		out.SourceRef = readSourceRef(o, obj.GetNamespace(), "spec", "sourceRef")
		entries, _, _ := unstructured.NestedSlice(o, "status", "inventory", "entries")
		out.InventoryCount = len(entries)
	case CategoryHelmRelease:
		out.Revision = nestedStr(o, "status", "lastAppliedRevision")
		out.AttemptedRevision = nestedStr(o, "status", "lastAttemptedRevision")
		out.Chart = nestedStr(o, "spec", "chart", "spec", "chart")
		out.ChartVersion = nestedStr(o, "spec", "chart", "spec", "version")
		out.SourceRef = readSourceRef(o, obj.GetNamespace(), "spec", "chart", "spec", "sourceRef")
		if out.SourceRef == nil {
			out.SourceRef = readSourceRef(o, obj.GetNamespace(), "spec", "chartRef")
		}
		hist := readHelmHistory(o)
		if len(hist) > 0 {
			if out.Chart == "" {
				out.Chart = hist[0].ChartName
			}
			if out.Revision == "" {
				out.Revision = hist[0].ChartVersion
			}
		}
		if out.Revision == "" {
			out.Revision = nestedStr(o, "status", "lastAttemptedRevision")
		}
	}

	conds := ReadConditions(obj)
	ready := FindCondition(conds, "Ready")
	reconciling := FindCondition(conds, "Reconciling")
	if ready != nil {
		out.Ready = ready.Status
		out.Reason = ready.Reason
		out.Message = ready.Message
		out.LastReconciledAt = ready.LastTransitionTime
	}
	out.Status = deriveStatus(out.Suspended, ready, reconciling)
	return out
}

func deriveStatus(suspended bool, ready, reconciling *Condition) string {
	if suspended {
		return StatusSuspended
	}
	if reconciling != nil && reconciling.Status == "True" {
		return StatusProgressing
	}
	if ready == nil {
		return StatusUnknown
	}
	switch ready.Status {
	case "True":
		return StatusReady
	case "False":
		switch ready.Reason {
		case "Progressing", "DependencyNotReady", "ArtifactInStorage":
			return StatusProgressing
		}
		return StatusFailed
	default:
		return StatusProgressing
	}
}

func readSourceRef(o map[string]any, defaultNS string, path ...string) *ObjectRef {
	m, found, _ := unstructured.NestedMap(o, path...)
	if !found {
		return nil
	}
	ref := &ObjectRef{Kind: str(m["kind"]), Name: str(m["name"]), Namespace: str(m["namespace"]), APIVersion: str(m["apiVersion"])}
	if ref.Name == "" || ref.Kind == "" {
		return nil
	}
	if ref.Namespace == "" {
		ref.Namespace = defaultNS
	}
	if ref.APIVersion == "" {
		ref.APIVersion = GroupSource + "/v1"
	}
	return ref
}

func readHelmHistory(o map[string]any) []HelmHistory {
	raw, _, _ := unstructured.NestedSlice(o, "status", "history")
	out := make([]HelmHistory, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		h := HelmHistory{
			ChartName:     str(m["chartName"]),
			ChartVersion:  str(m["chartVersion"]),
			AppVersion:    str(m["appVersion"]),
			Status:        str(m["status"]),
			FirstDeployed: str(m["firstDeployed"]),
			LastDeployed:  str(m["lastDeployed"]),
		}
		switch v := m["version"].(type) {
		case int64:
			h.Version = v
		case float64:
			h.Version = int64(v)
		}
		out = append(out, h)
	}
	return out
}

// ParseInventoryID splits a Flux inventory id ("<ns>_<name>_<group>_<kind>")
// into a reference. Kubernetes names never contain "_", so the split is exact.
func ParseInventoryID(id, version string) (ObjectRef, bool) {
	parts := strings.Split(id, "_")
	if len(parts) != 4 || parts[1] == "" || parts[3] == "" {
		return ObjectRef{}, false
	}
	apiVersion := version
	if parts[2] != "" {
		apiVersion = parts[2] + "/" + version
	}
	return ObjectRef{Namespace: parts[0], Name: parts[1], Kind: parts[3], APIVersion: apiVersion}, true
}

// ReadInventory returns the objects a Kustomization applied.
func ReadInventory(obj *unstructured.Unstructured) []ObjectRef {
	entries, _, _ := unstructured.NestedSlice(obj.Object, "status", "inventory", "entries")
	out := make([]ObjectRef, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := ParseInventoryID(str(m["id"]), str(m["v"])); ok {
			out = append(out, ref)
		}
	}
	return out
}

// ListFlux lists every detected Flux kind across all namespaces, in
// parallel. Kinds the caller cannot list (RBAC) are skipped, not fatal.
func ListFlux(ctx context.Context, dyn dynamic.Interface, det *FluxDetection) ([]FluxObject, error) {
	if dyn == nil || det == nil || !det.Installed {
		return []FluxObject{}, nil
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		out  = []FluxObject{}
		errs []error
	)
	for _, k := range det.Kinds {
		wg.Add(1)
		go func(k FluxKind) {
			defer wg.Done()
			list, err := dyn.Resource(k.GVR()).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !apierrors.IsForbidden(err) && !apierrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("list %s: %w", k.Plural, err))
				}
				return
			}
			for i := range list.Items {
				out = append(out, ParseFluxObject(k, &list.Items[i]))
			}
		}(k)
	}
	wg.Wait()
	SortFlux(out)
	if len(out) == 0 && len(errs) > 0 {
		return out, errs[0]
	}
	return out, nil
}

// SortFlux orders by category (sources first), then namespace and name.
func SortFlux(items []FluxObject) {
	rank := map[Category]int{CategoryKustomization: 0, CategoryHelmRelease: 1, CategorySource: 2}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if rank[a.Category] != rank[b.Category] {
			return rank[a.Category] < rank[b.Category]
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Kind < b.Kind
	})
}

// GetFluxObject reads one Flux object with conditions and inventory.
func GetFluxObject(ctx context.Context, dyn dynamic.Interface, det *FluxDetection, kind, namespace, name string) (*FluxObjectDetail, error) {
	k, ok := det.Kind(kind)
	if !ok {
		return nil, fmt.Errorf("flux kind %q is not installed", kind)
	}
	obj, err := dyn.Resource(k.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	d := &FluxObjectDetail{FluxObject: ParseFluxObject(k, obj), Conditions: ReadConditions(obj), Inventory: ReadInventory(obj)}
	if k.Category == CategoryHelmRelease {
		d.History = readHelmHistory(obj.Object)
	}
	return d, nil
}

// FluxSummary counts Flux objects by status and category.
type FluxSummary struct {
	Total      int            `json:"total"`
	ByStatus   map[string]int `json:"byStatus"`
	ByCategory map[string]int `json:"byCategory"`
}

// SummarizeFlux counts items.
func SummarizeFlux(items []FluxObject) FluxSummary {
	s := FluxSummary{ByStatus: map[string]int{}, ByCategory: map[string]int{}}
	for _, it := range items {
		s.Total++
		s.ByStatus[it.Status]++
		s.ByCategory[string(it.Category)]++
	}
	return s
}
