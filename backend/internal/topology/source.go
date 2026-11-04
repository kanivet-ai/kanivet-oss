package topology

import (
	"context"
	"fmt"
	"sort"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
)

// KindSpec describes one API kind the engine reads.
type KindSpec struct {
	Group, Version, Kind, Resource string
	Namespaced                     bool
	// MetadataOnly kinds are read through the metadata API, so their payload
	// (Secret .data) never leaves the cluster.
	MetadataOnly bool
}

func (k KindSpec) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: k.Group, Version: k.Version, Resource: k.Resource}
}

// Kinds the engine lists per namespace.
var namespacedKinds = []KindSpec{
	{"", "v1", "Pod", "pods", true, false},
	{"", "v1", "Service", "services", true, false},
	{"", "v1", "ConfigMap", "configmaps", true, true},
	{"", "v1", "Secret", "secrets", true, true},
	{"", "v1", "PersistentVolumeClaim", "persistentvolumeclaims", true, false},
	{"", "v1", "ServiceAccount", "serviceaccounts", true, true},
	{"apps", "v1", "Deployment", "deployments", true, false},
	{"apps", "v1", "ReplicaSet", "replicasets", true, false},
	{"apps", "v1", "StatefulSet", "statefulsets", true, false},
	{"apps", "v1", "DaemonSet", "daemonsets", true, false},
	{"batch", "v1", "Job", "jobs", true, false},
	{"batch", "v1", "CronJob", "cronjobs", true, false},
	{"networking.k8s.io", "v1", "Ingress", "ingresses", true, false},
	{"networking.k8s.io", "v1", "NetworkPolicy", "networkpolicies", true, false},
	{"autoscaling", "v2", "HorizontalPodAutoscaler", "horizontalpodautoscalers", true, false},
	{"policy", "v1", "PodDisruptionBudget", "poddisruptionbudgets", true, false},
	{"gateway.networking.k8s.io", "v1", "HTTPRoute", "httproutes", true, false},
	{"gateway.networking.k8s.io", "v1", "Gateway", "gateways", true, false},
}

// Cluster-scoped kinds, fetched by name only when something references them.
var (
	kindNode         = KindSpec{"", "v1", "Node", "nodes", false, false}
	kindPV           = KindSpec{"", "v1", "PersistentVolume", "persistentvolumes", false, false}
	kindStorageClass = KindSpec{"storage.k8s.io", "v1", "StorageClass", "storageclasses", false, false}
)

var specByKind = func() map[string]KindSpec {
	m := map[string]KindSpec{}
	for _, k := range namespacedKinds {
		m[k.Kind] = k
	}
	for _, k := range []KindSpec{kindNode, kindPV, kindStorageClass} {
		m[k.Kind] = k
	}
	return m
}()

// SpecFor returns the KindSpec the engine uses for kind, if it knows it.
func SpecFor(kind string) (KindSpec, bool) {
	k, ok := specByKind[kind]
	return k, ok
}

// Source reads objects. Implementations must never return Secret .data for
// MetadataOnly kinds.
type Source interface {
	List(ctx context.Context, kind KindSpec, namespace string) ([]*unstructured.Unstructured, error)
	Get(ctx context.Context, kind KindSpec, namespace, name string) (*unstructured.Unstructured, error)
}

// ClientSource reads through a dynamic client, and a metadata client for
// MetadataOnly kinds.
type ClientSource struct {
	Dynamic  dynamic.Interface
	Metadata metadata.Interface
}

func (s *ClientSource) List(ctx context.Context, k KindSpec, namespace string) ([]*unstructured.Unstructured, error) {
	if k.MetadataOnly && s.Metadata != nil {
		list, err := s.Metadata.Resource(k.GVR()).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out := make([]*unstructured.Unstructured, 0, len(list.Items))
		for i := range list.Items {
			m := list.Items[i].ObjectMeta
			u := &unstructured.Unstructured{Object: map[string]interface{}{}}
			u.SetAPIVersion(schema.GroupVersion{Group: k.Group, Version: k.Version}.String())
			u.SetKind(k.Kind)
			u.SetName(m.Name)
			u.SetNamespace(m.Namespace)
			u.SetUID(m.UID)
			u.SetOwnerReferences(m.OwnerReferences)
			u.SetLabels(m.Labels)
			// Only the Helm release name: other annotations (last-applied
			// configuration) can carry a Secret's payload.
			if rel := m.Annotations["meta.helm.sh/release-name"]; rel != "" {
				u.SetAnnotations(map[string]string{"meta.helm.sh/release-name": rel})
			}
			out = append(out, u)
		}
		return out, nil
	}
	if k.Kind == "Secret" {
		return nil, fmt.Errorf("refusing to list secrets without a metadata client")
	}
	list, err := s.Dynamic.Resource(k.GVR()).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]*unstructured.Unstructured, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

func (s *ClientSource) Get(ctx context.Context, k KindSpec, namespace, name string) (*unstructured.Unstructured, error) {
	r := s.Dynamic.Resource(k.GVR())
	if k.Namespaced {
		return r.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return r.Get(ctx, name, metav1.GetOptions{})
}

// Snapshot is every object of the engine's kinds in one namespace, plus the
// cluster-scoped objects they reference.
type Snapshot struct {
	Namespace string
	objects   map[string]*unstructured.Unstructured // by Ref.ID()
	byKind    map[string][]*unstructured.Unstructured
	byUID     map[string]*unstructured.Unstructured
	warnings  []string
}

func newSnapshot(ns string) *Snapshot {
	return &Snapshot{
		Namespace: ns,
		objects:   map[string]*unstructured.Unstructured{},
		byKind:    map[string][]*unstructured.Unstructured{},
		byUID:     map[string]*unstructured.Unstructured{},
	}
}

func (s *Snapshot) add(u *unstructured.Unstructured, kind string) {
	if u.GetKind() == "" {
		u.SetKind(kind)
	}
	id := refOf(u).ID()
	if _, dup := s.objects[id]; dup {
		return
	}
	s.objects[id] = u
	s.byKind[kind] = append(s.byKind[kind], u)
	if uid := string(u.GetUID()); uid != "" {
		s.byUID[uid] = u
	}
}

// Get returns the object with the given node ID.
func (s *Snapshot) Get(id string) *unstructured.Unstructured { return s.objects[id] }

const loadConcurrency = 8

// LoadSnapshot reads one namespace through src. Kinds whose API is absent are
// skipped; other per-kind errors become warnings rather than failing the
// whole graph, so a user without access to Secrets still gets a topology.
func LoadSnapshot(ctx context.Context, src Source, namespace string) (*Snapshot, error) {
	snap := newSnapshot(namespace)
	type result struct {
		kind  KindSpec
		items []*unstructured.Unstructured
		err   error
	}
	results := make([]result, len(namespacedKinds))
	var wg sync.WaitGroup
	sem := make(chan struct{}, loadConcurrency)
	for i, k := range namespacedKinds {
		wg.Add(1)
		go func(i int, k KindSpec) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items, err := src.List(ctx, k, namespace)
			results[i] = result{k, items, err}
		}(i, k)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	failed := 0
	for _, r := range results {
		if r.err != nil {
			if !apierrors.IsNotFound(r.err) {
				failed++
				snap.warnings = append(snap.warnings, warningFor(r.kind.Kind, r.err))
			}
			continue
		}
		for _, u := range r.items {
			snap.add(u, r.kind.Kind)
		}
	}
	if failed == len(namespacedKinds) {
		return nil, fmt.Errorf("could not read namespace %q: %s", namespace, snap.warnings[0])
	}
	loadClusterScoped(ctx, src, snap)
	sort.Strings(snap.warnings)
	return snap, nil
}

func warningFor(kind string, err error) string {
	if apierrors.IsForbidden(err) {
		return kind + ": access denied"
	}
	return kind + ": " + err.Error()
}

// loadClusterScoped fetches the Nodes, PVs and StorageClasses the namespace
// references. Missing or forbidden ones are left out; the edge still gets a
// placeholder node.
func loadClusterScoped(ctx context.Context, src Source, snap *Snapshot) {
	want := map[KindSpec]map[string]bool{kindNode: {}, kindPV: {}, kindStorageClass: {}}
	for _, p := range snap.byKind["Pod"] {
		if n, _, _ := unstructured.NestedString(p.Object, "spec", "nodeName"); n != "" {
			want[kindNode][n] = true
		}
	}
	for _, c := range snap.byKind["PersistentVolumeClaim"] {
		if v, _, _ := unstructured.NestedString(c.Object, "spec", "volumeName"); v != "" {
			want[kindPV][v] = true
		}
		if sc, _, _ := unstructured.NestedString(c.Object, "spec", "storageClassName"); sc != "" {
			want[kindStorageClass][sc] = true
		}
	}
	fetch := func(k KindSpec) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, loadConcurrency)
		names := make([]string, 0, len(want[k]))
		for n := range want[k] {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) > 200 {
			names = names[:200]
		}
		for _, n := range names {
			wg.Add(1)
			go func(n string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				u, err := src.Get(ctx, k, "", n)
				if err != nil || u == nil {
					return
				}
				mu.Lock()
				snap.add(u, k.Kind)
				mu.Unlock()
			}(n)
		}
		wg.Wait()
	}
	fetch(kindNode)
	fetch(kindPV)
	// PVs name their StorageClass too.
	for _, pv := range snap.byKind["PersistentVolume"] {
		if sc, _, _ := unstructured.NestedString(pv.Object, "spec", "storageClassName"); sc != "" {
			want[kindStorageClass][sc] = true
		}
	}
	fetch(kindStorageClass)
}

func refOf(u *unstructured.Unstructured) Ref {
	gv, _ := schema.ParseGroupVersion(u.GetAPIVersion())
	k := u.GetKind()
	if spec, ok := specByKind[k]; ok && gv.Version == "" {
		gv = schema.GroupVersion{Group: spec.Group, Version: spec.Version}
	}
	return Ref{Group: gv.Group, Version: gv.Version, Kind: k, Namespace: u.GetNamespace(), Name: u.GetName()}
}
