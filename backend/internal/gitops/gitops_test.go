package gitops

import (
	"context"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/argo"
	"github.com/kanivet/backend/internal/crdprobe"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
)

func crd(plural, group string, versions ...map[string]any) *unstructured.Unstructured {
	vs := make([]any, 0, len(versions))
	for _, v := range versions {
		vs = append(vs, v)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": plural + "." + group},
		"spec":       map[string]any{"versions": vs},
	}}
}

func ver(name string, served, storage bool) map[string]any {
	return map[string]any{"name": name, "served": served, "storage": storage}
}

func kustomization(ns, name string, suspend bool, ready, reason string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
		"kind":       "Kustomization",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"suspend":   suspend,
			"interval":  "10m",
			"path":      "./apps",
			"sourceRef": map[string]any{"kind": "GitRepository", "name": "flux-system"},
		},
		"status": map[string]any{
			"lastAppliedRevision": "main@sha1:abc",
			"conditions": []any{
				map[string]any{"type": "Ready", "status": ready, "reason": reason, "message": "m", "lastTransitionTime": "2026-01-01T00:00:00Z"},
			},
			"inventory": map[string]any{"entries": []any{
				map[string]any{"id": "default_web_apps_Deployment", "v": "v1"},
				map[string]any{"id": "_ns1__Namespace", "v": "v1"},
				map[string]any{"id": "bad", "v": "v1"},
			}},
		},
	}}
}

func gitRepo(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "source.toolkit.fluxcd.io/v1",
		"kind":       "GitRepository",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"url": "https://example.com/repo"},
		"status": map[string]any{
			"artifact":   map[string]any{"revision": "main@sha1:abc"},
			"conditions": []any{map[string]any{"type": "Ready", "status": "True", "reason": "Succeeded"}},
		},
	}}
}

func newFake(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrs := map[schema.GroupVersionResource]string{
		crdprobe.CRDGVR: "CustomResourceDefinitionList",
		{Group: GroupKustomize, Version: "v1", Resource: "kustomizations"}: "KustomizationList",
		{Group: GroupSource, Version: "v1", Resource: "gitrepositories"}:   "GitRepositoryList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrs, objs...)
}

func TestServedVersionPrefersStorage(t *testing.T) {
	c := crd("x", "g", ver("v1beta1", true, false), ver("v1", true, true))
	if v := crdprobe.ServedVersion(c); v != "v1" {
		t.Fatalf("got %q", v)
	}
	c = crd("x", "g", ver("v1beta1", true, false), ver("v1", false, true))
	if v := crdprobe.ServedVersion(c); v != "v1beta1" {
		t.Fatalf("got %q", v)
	}
}

func TestDetectFlux(t *testing.T) {
	ctx := context.Background()
	det, err := DetectFlux(ctx, newFake())
	if err != nil || det.Installed {
		t.Fatalf("empty cluster: det=%+v err=%v", det, err)
	}
	dyn := newFake(
		crd("kustomizations", GroupKustomize, ver("v1", true, true)),
		crd("gitrepositories", GroupSource, ver("v1", true, true)),
	)
	det, err = DetectFlux(ctx, dyn)
	if err != nil || !det.Installed || len(det.Kinds) != 2 {
		t.Fatalf("det=%+v err=%v", det, err)
	}
	if k, ok := det.Kind("kustomization"); !ok || k.Version != "v1" {
		t.Fatalf("kind lookup: %+v %v", k, ok)
	}
}

func TestParseFluxObjectStatuses(t *testing.T) {
	k := KnownFluxKinds[4]
	k.Version = "v1"
	cases := []struct {
		suspend       bool
		ready, reason string
		want          string
	}{
		{false, "True", "ReconciliationSucceeded", StatusReady},
		{false, "False", "ReconciliationFailed", StatusFailed},
		{false, "False", "DependencyNotReady", StatusProgressing},
		{false, "Unknown", "Progressing", StatusProgressing},
		{true, "True", "x", StatusSuspended},
	}
	for _, c := range cases {
		o := ParseFluxObject(k, kustomization("flux-system", "apps", c.suspend, c.ready, c.reason))
		if o.Status != c.want {
			t.Errorf("%+v: got %s", c, o.Status)
		}
		if o.Revision != "main@sha1:abc" || o.InventoryCount != 3 || o.SourceRef == nil || o.SourceRef.Namespace != "flux-system" {
			t.Errorf("fields: %+v", o)
		}
	}
}

func TestInventory(t *testing.T) {
	refs := ReadInventory(kustomization("flux-system", "apps", false, "True", ""))
	if len(refs) != 2 {
		t.Fatalf("got %d refs: %+v", len(refs), refs)
	}
	if refs[0] != (ObjectRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "default", Name: "web"}) {
		t.Errorf("deployment ref: %+v", refs[0])
	}
	if refs[1] != (ObjectRef{APIVersion: "v1", Kind: "Namespace", Name: "ns1"}) {
		t.Errorf("namespace ref: %+v", refs[1])
	}
}

func TestReconcileAndSuspend(t *testing.T) {
	ctx := context.Background()
	dyn := newFake(kustomization("flux-system", "apps", false, "True", ""), gitRepo("flux-system", "flux-system"))
	det := &FluxDetection{Installed: true, Kinds: []FluxKind{
		{Kind: "GitRepository", Plural: "gitrepositories", Group: GroupSource, Version: "v1", Category: CategorySource},
		{Kind: "Kustomization", Plural: "kustomizations", Group: GroupKustomize, Version: "v1", Category: CategoryKustomization},
	}}
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	res, err := FluxReconcile(ctx, dyn, det, "Kustomization", "flux-system", "apps", ReconcileOptions{WithSource: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Objects) != 2 || res.Objects[0].Kind != "GitRepository" {
		t.Fatalf("objects: %+v", res.Objects)
	}
	got, _ := dyn.Resource(det.Kinds[1].GVR()).Namespace("flux-system").Get(ctx, "apps", metav1.GetOptions{})
	if got.GetAnnotations()[ReconcileAnnotation] != now.Format(time.RFC3339Nano) {
		t.Fatalf("annotation: %v", got.GetAnnotations())
	}

	if err := FluxSetSuspended(ctx, dyn, det, "Kustomization", "flux-system", "apps", true); err != nil {
		t.Fatal(err)
	}
	if _, err := FluxReconcile(ctx, dyn, det, "Kustomization", "flux-system", "apps", ReconcileOptions{}); err == nil {
		t.Fatal("reconcile of a suspended object must be refused")
	}
	list, _ := ListFlux(ctx, dyn, det)
	if len(list) != 2 || list[0].Kind != "Kustomization" || list[0].Status != StatusSuspended {
		t.Fatalf("list: %+v", list)
	}
}

func TestOwnerOf(t *testing.T) {
	o := OwnerOf(map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil, "", false)
	if o == nil || o.Tool != "flux" || o.Ref.Kind != "Kustomization" || o.Ref.Namespace != "flux-system" {
		t.Fatalf("kustomize: %+v", o)
	}
	o = OwnerOf(map[string]string{"helm.toolkit.fluxcd.io/name": "redis", "helm.toolkit.fluxcd.io/namespace": "db", "kustomize.toolkit.fluxcd.io/name": "x"}, nil, "", false)
	if o == nil || o.Ref.Kind != "HelmRelease" {
		t.Fatalf("helm wins: %+v", o)
	}
	o = OwnerOf(nil, map[string]string{"argocd.argoproj.io/tracking-id": "guestbook:apps/Deployment:default/web"}, "gitops", false)
	if o == nil || o.Tool != "argocd" || o.Ref.Name != "guestbook" || o.Ref.Namespace != "gitops" {
		t.Fatalf("argo tracking: %+v", o)
	}
	o = OwnerOf(nil, map[string]string{"argocd.argoproj.io/tracking-id": "team-a_guestbook:apps/Deployment:default/web"}, "", false)
	if o == nil || o.Ref.Namespace != "team-a" || o.Ref.Name != "guestbook" {
		t.Fatalf("apps in any namespace: %+v", o)
	}
	if o := OwnerOf(map[string]string{"app.kubernetes.io/instance": "redis"}, nil, "", false); o != nil {
		t.Fatalf("instance label must not be trusted by default: %+v", o)
	}
	if o := OwnerOf(map[string]string{"app.kubernetes.io/instance": "redis"}, nil, "", true); o == nil || o.Ref.Namespace != "argocd" {
		t.Fatalf("instance label when trusted: %+v", o)
	}
}

func TestBuildStatus(t *testing.T) {
	apps := []argo.AppListEntry{
		{Name: "ok", Namespace: "argocd", SyncStatus: "Synced", Health: "Healthy"},
		{Name: "bad", Namespace: "argocd", SyncStatus: "OutOfSync", Health: "Degraded"},
	}
	flux := []FluxObject{{Kind: "Kustomization", Name: "apps", Namespace: "flux-system", Status: StatusFailed}}
	st := BuildStatus(true, apps, true, flux)
	if len(st.Items) != 3 || st.ByHealth[HealthFailing] != 2 || st.ByHealth[HealthHealthy] != 1 {
		t.Fatalf("status: %+v", st)
	}
	if st.Items[len(st.Items)-1].Name != "ok" {
		t.Fatalf("failing rows should sort first: %+v", st.Items)
	}
	if ArgoHealth("OutOfSync", "Healthy") != HealthProgressing {
		t.Fatal("out of sync healthy app is progressing")
	}
}
