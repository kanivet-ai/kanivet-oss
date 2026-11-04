package upgrade

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/helm"
	"github.com/kanivet/backend/internal/k8s"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// HelmSource is the part of the Helm service the readiness check reads.
type HelmSource interface {
	ListReleases(ctx context.Context, cluster, namespace string, allNamespaces bool) ([]helm.Release, error)
	GetReleaseManifest(ctx context.Context, cluster, namespace, name string) (string, error)
}

const (
	listPageSize      = 500
	maxObjectsPerKind = 20000
	helmConcurrency   = 6
	maxMetricsBytes   = 32 << 20
)

// skipLastAppliedKinds are never listed for annotations: events are
// ephemeral and numerous, and nobody applies them from manifests.
var skipLastAppliedKinds = map[string]bool{"Event": true}

type servedResource struct {
	Group, Version, Resource, Kind string
	Namespaced                     bool
	Listable                       bool
}

func (r servedResource) apiVersion() string {
	if r.Group == "" {
		return r.Version
	}
	return r.Group + "/" + r.Version
}

// collect gathers every evidence source. Each one degrades on its own: a
// forbidden or failing source becomes an unavailable Check, never an error.
func collect(ctx context.Context, cat *Catalog, client k8s.Interface, helmSrc HelmSource, cluster string) (*Inputs, error) {
	in := &Inputs{Served: map[string]bool{}, CollectedAt: time.Now()}

	cs, err := client.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	sv, err := cs.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("read the cluster version: %w", err)
	}
	in.ServerVersion = sv.GitVersion

	apiResources, err := client.ListAPIResources(cluster)
	if err != nil {
		in.Checks = append(in.Checks, Check{ID: "discovery", Label: "Served APIs", Status: CheckUnavailable, Detail: err.Error()})
	} else {
		in.Checks = append(in.Checks, Check{ID: "discovery", Label: "Served APIs", Status: CheckOK, Detail: fmt.Sprintf("%d resources discovered", len(apiResources))})
	}
	byKind := map[string][]servedResource{}
	for _, r := range apiResources {
		sr := servedResource{Group: r.Group, Version: r.Version, Resource: r.Name, Kind: r.Kind, Namespaced: r.Namespaced}
		for _, v := range r.Verbs {
			if v == "list" {
				sr.Listable = true
			}
		}
		in.Served[r.Group+"/"+r.Version+"/"+r.Name] = true
		byKind[r.Kind] = append(byKind[r.Kind], sr)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	add := func(objs []ObservedObject, chk Check) {
		mu.Lock()
		in.Objects = append(in.Objects, objs...)
		in.Checks = append(in.Checks, chk)
		mu.Unlock()
	}

	wg.Add(5)
	go func() {
		defer wg.Done()
		reqs, chk := collectRequests(ctx, cs.CoreV1().RESTClient().Get().AbsPath("/metrics").DoRaw)
		mu.Lock()
		in.Requests = reqs
		in.Checks = append(in.Checks, chk)
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		objs, chk := collectLastApplied(ctx, cat, client, cluster, byKind)
		add(objs, chk)
	}()
	go func() {
		defer wg.Done()
		objs, chk := collectHelm(ctx, cat, helmSrc, cluster, byKind)
		add(objs, chk)
	}()
	go func() {
		defer wg.Done()
		nodes, chk := collectNodes(ctx, client, cluster)
		mu.Lock()
		in.Nodes = nodes
		in.Checks = append(in.Checks, chk)
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		crds, chk := collectCRDs(ctx, client, cluster)
		mu.Lock()
		in.CRDs = crds
		in.Checks = append(in.Checks, chk)
		mu.Unlock()
	}()
	wg.Wait()

	order := map[string]int{"discovery": 0, "metrics": 1, "last-applied": 2, "helm": 3, "nodes": 4, "crds": 5}
	sort.SliceStable(in.Checks, func(i, j int) bool { return order[in.Checks[i].ID] < order[in.Checks[j].ID] })
	return in, nil
}

func describeErr(err error) string {
	switch {
	case apierrors.IsForbidden(err):
		return "Not permitted by RBAC"
	case apierrors.IsNotFound(err):
		return "Not exposed by this cluster"
	}
	return err.Error()
}

func collectRequests(ctx context.Context, get func(context.Context) ([]byte, error)) ([]DeprecatedRequest, Check) {
	chk := Check{ID: "metrics", Label: "Deprecated API requests (API server metrics)"}
	raw, err := get(ctx)
	if err != nil {
		chk.Status = CheckUnavailable
		chk.Detail = describeErr(err) + ". Live client usage is not checked; manifests still are."
		return nil, chk
	}
	if len(raw) > maxMetricsBytes {
		raw = raw[:maxMetricsBytes]
	}
	reqs := ParseDeprecatedRequests(raw)
	chk.Status = CheckOK
	chk.Detail = fmt.Sprintf("%d deprecated APIs requested since the API server started", len(reqs))
	return reqs, chk
}

// listTargets picks, for each kind in the catalog, the served resource to
// list: the replacement version when served, else any served version.
func listTargets(cat *Catalog, byKind map[string][]servedResource) []servedResource {
	var out []servedResource
	done := map[string]bool{}
	for _, d := range cat.APIs {
		if done[d.Kind] || skipLastAppliedKinds[d.Kind] {
			continue
		}
		cands := byKind[d.Kind]
		var pick *servedResource
		for i := range cands {
			c := cands[i]
			if !c.Listable {
				continue
			}
			if c.apiVersion() == d.Replacement {
				pick = &cands[i]
				break
			}
			if pick == nil && sameKindGroup(cat, d.Kind, c.Group) {
				pick = &cands[i]
			}
		}
		if pick == nil {
			continue
		}
		done[d.Kind] = true
		out = append(out, *pick)
	}
	return out
}

// sameKindGroup: the group is one the catalog associates with the kind, so a
// CRD that happens to be called "Ingress" is never mistaken for the built-in.
func sameKindGroup(cat *Catalog, kind, group string) bool {
	for _, d := range cat.APIs {
		if d.Kind != kind {
			continue
		}
		if d.Group == group || strings.HasPrefix(d.Replacement, group+"/") || (group == "" && !strings.Contains(d.Replacement, "/")) {
			return true
		}
	}
	return false
}

func collectLastApplied(ctx context.Context, cat *Catalog, client k8s.Interface, cluster string, byKind map[string][]servedResource) ([]ObservedObject, Check) {
	chk := Check{ID: "last-applied", Label: "last-applied-configuration annotations"}
	mc, err := client.GetBulkMetadataClient(cluster)
	if err != nil {
		chk.Status = CheckUnavailable
		chk.Detail = err.Error()
		return nil, chk
	}
	targets := listTargets(cat, byKind)
	var out []ObservedObject
	var denied []string
	scanned := 0
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		gvr := schema.GroupVersionResource{Group: t.Group, Version: t.Version, Resource: t.Resource}
		cont := ""
		count := 0
		for {
			lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			list, err := mc.Resource(gvr).List(lctx, metav1.ListOptions{Limit: listPageSize, Continue: cont})
			cancel()
			if err != nil {
				denied = append(denied, t.Kind)
				break
			}
			for _, item := range list.Items {
				count++
				av := LastAppliedAPIVersion(item.Annotations)
				obj := ObservedObject{
					LiveAPIVersion: t.apiVersion(),
					Kind:           t.Kind,
					Name:           item.Name,
					Namespace:      item.Namespace,
				}
				if av != "" {
					if _, ok := cat.Lookup(av, t.Kind); ok {
						o := obj
						o.APIVersion = av
						o.Source = SourceLastApplied
						out = append(out, o)
					}
				}
				// A kind served only in a deprecated version with no
				// replacement (PodSecurityPolicy) is lost on removal.
				if d, ok := cat.Lookup(t.apiVersion(), t.Kind); ok && d.Replacement == "" {
					o := obj
					o.APIVersion = t.apiVersion()
					o.Source = SourceLive
					out = append(out, o)
				}
			}
			cont = list.Continue
			if cont == "" || count >= maxObjectsPerKind {
				break
			}
		}
		scanned += count
	}
	switch {
	case len(denied) > 0 && len(denied) == len(targets):
		chk.Status = CheckUnavailable
		chk.Detail = "Could not list any of the checked kinds"
	case len(denied) > 0:
		chk.Status = CheckPartial
		chk.Detail = fmt.Sprintf("%d objects scanned; could not list %s", scanned, strings.Join(denied, ", "))
	default:
		chk.Status = CheckOK
		chk.Detail = fmt.Sprintf("%d objects across %d kinds scanned", scanned, len(targets))
	}
	return out, chk
}

func collectHelm(ctx context.Context, cat *Catalog, src HelmSource, cluster string, byKind map[string][]servedResource) ([]ObservedObject, Check) {
	chk := Check{ID: "helm", Label: "Helm release manifests"}
	if src == nil {
		chk.Status = CheckUnavailable
		chk.Detail = "Helm is not available"
		return nil, chk
	}
	releases, err := src.ListReleases(ctx, cluster, "", true)
	if err != nil {
		chk.Status = CheckUnavailable
		chk.Detail = describeErr(err)
		return nil, chk
	}
	type result struct {
		objs []ObservedObject
		err  error
	}
	results := make([]result, len(releases))
	sem := make(chan struct{}, helmConcurrency)
	var wg sync.WaitGroup
	for i, rel := range releases {
		wg.Add(1)
		go func(i int, rel helm.Release) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			manifest, err := src.GetReleaseManifest(ctx, cluster, rel.Namespace, rel.Name)
			if err != nil {
				results[i].err = err
				return
			}
			results[i].objs = helmObjects(cat, rel, manifest, byKind)
		}(i, rel)
	}
	wg.Wait()
	var out []ObservedObject
	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
			continue
		}
		out = append(out, r.objs...)
	}
	switch {
	case failed > 0 && failed == len(releases):
		chk.Status = CheckUnavailable
		chk.Detail = fmt.Sprintf("Could not read any of %d release manifests", len(releases))
	case failed > 0:
		chk.Status = CheckPartial
		chk.Detail = fmt.Sprintf("%d of %d release manifests read", len(releases)-failed, len(releases))
	default:
		chk.Status = CheckOK
		chk.Detail = fmt.Sprintf("%d releases checked", len(releases))
	}
	return out, chk
}

// helmObjects returns the deprecated objects in one release's manifest.
func helmObjects(cat *Catalog, rel helm.Release, manifest string, byKind map[string][]servedResource) []ObservedObject {
	var out []ObservedObject
	for _, m := range ParseManifest(manifest) {
		d, ok := cat.Lookup(m.APIVersion, m.Kind)
		if !ok {
			continue
		}
		o := ObservedObject{
			APIVersion:    m.APIVersion,
			Kind:          m.Kind,
			Name:          m.Name,
			Namespace:     m.Namespace,
			Source:        SourceHelm,
			HelmRelease:   rel.Name,
			HelmNamespace: rel.Namespace,
		}
		// Open the object through a version the cluster still serves.
		var namespaced bool
		for _, s := range byKind[m.Kind] {
			if s.apiVersion() == d.Replacement || (o.LiveAPIVersion == "" && s.apiVersion() == m.APIVersion) {
				o.LiveAPIVersion = s.apiVersion()
				namespaced = s.Namespaced
			}
		}
		if o.Namespace == "" && namespaced {
			o.Namespace = rel.Namespace
		}
		out = append(out, o)
	}
	return out
}

func collectNodes(ctx context.Context, client k8s.Interface, cluster string) ([]NodeInfo, Check) {
	chk := Check{ID: "nodes", Label: "Kubelet version skew"}
	cs, err := client.GetClientForCluster(cluster)
	if err != nil {
		chk.Status = CheckUnavailable
		chk.Detail = err.Error()
		return nil, chk
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	nodes, err := cs.CoreV1().Nodes().List(lctx, metav1.ListOptions{})
	if err != nil {
		chk.Status = CheckUnavailable
		chk.Detail = describeErr(err)
		return nil, chk
	}
	out := make([]NodeInfo, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		out = append(out, NodeInfo{Name: n.Name, KubeletVersion: n.Status.NodeInfo.KubeletVersion})
	}
	chk.Status = CheckOK
	chk.Detail = fmt.Sprintf("%d nodes", len(out))
	return out, chk
}

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

func collectCRDs(ctx context.Context, client k8s.Interface, cluster string) ([]CRDInfo, Check) {
	chk := Check{ID: "crds", Label: "Custom resource versions"}
	dc, err := client.GetDynamicClient(cluster)
	if err != nil {
		chk.Status = CheckUnavailable
		chk.Detail = err.Error()
		return nil, chk
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := dc.Resource(crdGVR).List(lctx, metav1.ListOptions{})
	if err != nil {
		chk.Status = CheckUnavailable
		chk.Detail = describeErr(err)
		return nil, chk
	}
	out := make([]CRDInfo, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, crdInfo(item.Object))
	}
	chk.Status = CheckOK
	chk.Detail = fmt.Sprintf("%d CRDs", len(out))
	return out, chk
}

func crdInfo(obj map[string]any) CRDInfo {
	c := CRDInfo{}
	c.Name, _, _ = unstructured.NestedString(obj, "metadata", "name")
	c.Group, _, _ = unstructured.NestedString(obj, "spec", "group")
	c.Kind, _, _ = unstructured.NestedString(obj, "spec", "names", "kind")
	c.StoredVersions, _, _ = unstructured.NestedStringSlice(obj, "status", "storedVersions")
	versions, _, _ := unstructured.NestedSlice(obj, "spec", "versions")
	for _, v := range versions {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		cv := CRDVersion{}
		cv.Name, _, _ = unstructured.NestedString(m, "name")
		cv.Served, _, _ = unstructured.NestedBool(m, "served")
		cv.Storage, _, _ = unstructured.NestedBool(m, "storage")
		cv.Deprecated, _, _ = unstructured.NestedBool(m, "deprecated")
		cv.DeprecationWarning, _, _ = unstructured.NestedString(m, "deprecationWarning")
		c.Versions = append(c.Versions, cv)
	}
	return c
}
