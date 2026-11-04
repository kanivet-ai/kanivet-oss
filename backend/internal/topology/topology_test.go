package topology

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeSource struct {
	objs   []*unstructured.Unstructured
	errs   map[string]error // by Kind
	mu    sync.Mutex
	lists int
}

func (f *fakeSource) List(_ context.Context, k KindSpec, ns string) ([]*unstructured.Unstructured, error) {
	f.mu.Lock()
	f.lists++
	f.mu.Unlock()
	if err := f.errs[k.Kind]; err != nil {
		return nil, err
	}
	var out []*unstructured.Unstructured
	for _, o := range f.objs {
		if o.GetKind() == k.Kind && (ns == "" || o.GetNamespace() == ns) {
			out = append(out, o.DeepCopy())
		}
	}
	return out, nil
}

func (f *fakeSource) Get(_ context.Context, k KindSpec, _, name string) (*unstructured.Unstructured, error) {
	for _, o := range f.objs {
		if o.GetKind() == k.Kind && o.GetName() == name {
			return o.DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Resource: k.Resource}, name)
}

func obj(apiVersion, kind, ns, name, uid string, rest map[string]interface{}) *unstructured.Unstructured {
	m := map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name, "uid": uid},
	}
	if ns != "" {
		m["metadata"].(map[string]interface{})["namespace"] = ns
	}
	for k, v := range rest {
		if k == "metadata" {
			for mk, mv := range v.(map[string]interface{}) {
				m["metadata"].(map[string]interface{})[mk] = mv
			}
			continue
		}
		m[k] = v
	}
	return &unstructured.Unstructured{Object: m}
}

func owner(kind, name, uid string) map[string]interface{} {
	return map[string]interface{}{"ownerReferences": []interface{}{map[string]interface{}{
		"apiVersion": "apps/v1", "kind": kind, "name": name, "uid": uid, "controller": true,
	}}}
}

func fixture() *fakeSource {
	podSpec := map[string]interface{}{
		"serviceAccountName": "web-sa",
		"volumes": []interface{}{
			map[string]interface{}{"name": "cfg", "configMap": map[string]interface{}{"name": "web-config"}},
			map[string]interface{}{"name": "data", "persistentVolumeClaim": map[string]interface{}{"claimName": "web-data"}},
		},
		"containers": []interface{}{map[string]interface{}{
			"name": "web",
			"envFrom": []interface{}{map[string]interface{}{"secretRef": map[string]interface{}{"name": "web-creds"}}},
			"env": []interface{}{map[string]interface{}{"name": "X", "valueFrom": map[string]interface{}{
				"secretKeyRef": map[string]interface{}{"name": "gone", "key": "k"}}}},
		}},
	}
	pod := func(name, uid, phase string, ready bool) *unstructured.Unstructured {
		meta := owner("ReplicaSet", "web-abc", "rs1")
		meta["labels"] = map[string]interface{}{"app": "web"}
		return obj("v1", "Pod", "ns", name, uid, map[string]interface{}{
			"metadata": meta,
			"spec":     map[string]interface{}{"nodeName": "node-1", "containers": []interface{}{}},
			"status": map[string]interface{}{"phase": phase, "containerStatuses": []interface{}{
				map[string]interface{}{"name": "web", "ready": ready},
			}},
		})
	}
	return &fakeSource{objs: []*unstructured.Unstructured{
		obj("apps/v1", "Deployment", "ns", "web", "d1", map[string]interface{}{
			"spec":   map[string]interface{}{"replicas": int64(2), "template": map[string]interface{}{"spec": podSpec}},
			"status": map[string]interface{}{"readyReplicas": int64(1)},
		}),
		obj("apps/v1", "ReplicaSet", "ns", "web-abc", "rs1", map[string]interface{}{
			"metadata": owner("Deployment", "web", "d1"),
			"spec":     map[string]interface{}{"replicas": int64(2)},
			"status":   map[string]interface{}{"replicas": int64(2), "readyReplicas": int64(1)},
		}),
		obj("apps/v1", "ReplicaSet", "ns", "web-old", "rs0", map[string]interface{}{
			"metadata": owner("Deployment", "web", "d1"),
			"spec":     map[string]interface{}{"replicas": int64(0)},
		}),
		pod("web-abc-1", "p1", "Running", true),
		pod("web-abc-2", "p2", "Pending", false),
		obj("v1", "Service", "ns", "web", "s1", map[string]interface{}{
			"spec": map[string]interface{}{"type": "ClusterIP", "selector": map[string]interface{}{"app": "web"}},
		}),
		obj("networking.k8s.io/v1", "Ingress", "ns", "web", "i1", map[string]interface{}{
			"spec": map[string]interface{}{
				"tls": []interface{}{map[string]interface{}{"secretName": "web-tls"}},
				"rules": []interface{}{map[string]interface{}{"http": map[string]interface{}{"paths": []interface{}{
					map[string]interface{}{"backend": map[string]interface{}{"service": map[string]interface{}{"name": "web"}}},
				}}}},
			},
		}),
		obj("gateway.networking.k8s.io/v1", "HTTPRoute", "ns", "web-route", "r1", map[string]interface{}{
			"spec": map[string]interface{}{
				"parentRefs": []interface{}{map[string]interface{}{"name": "gw"}},
				"rules":      []interface{}{map[string]interface{}{"backendRefs": []interface{}{map[string]interface{}{"name": "web"}}}},
			},
		}),
		obj("gateway.networking.k8s.io/v1", "Gateway", "ns", "gw", "g1", nil),
		obj("v1", "ConfigMap", "ns", "web-config", "c1", nil),
		obj("v1", "Secret", "ns", "web-creds", "sec1", nil),
		obj("v1", "Secret", "ns", "web-tls", "sec2", nil),
		obj("v1", "ServiceAccount", "ns", "web-sa", "sa1", nil),
		obj("v1", "PersistentVolumeClaim", "ns", "web-data", "pvc1", map[string]interface{}{
			"spec":   map[string]interface{}{"volumeName": "pv-1", "storageClassName": "fast"},
			"status": map[string]interface{}{"phase": "Bound"},
		}),
		obj("v1", "PersistentVolume", "", "pv-1", "pv1", map[string]interface{}{
			"spec": map[string]interface{}{"storageClassName": "fast"},
		}),
		obj("storage.k8s.io/v1", "StorageClass", "", "fast", "sc1", nil),
		obj("v1", "Node", "", "node-1", "n1", map[string]interface{}{
			"status": map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}),
		obj("autoscaling/v2", "HorizontalPodAutoscaler", "ns", "web", "h1", map[string]interface{}{
			"spec": map[string]interface{}{"maxReplicas": int64(5), "scaleTargetRef": map[string]interface{}{
				"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}},
		}),
		obj("policy/v1", "PodDisruptionBudget", "ns", "web", "pdb1", map[string]interface{}{
			"spec": map[string]interface{}{"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "web"}}},
		}),
		obj("networking.k8s.io/v1", "NetworkPolicy", "ns", "deny", "np1", map[string]interface{}{
			"spec": map[string]interface{}{"podSelector": map[string]interface{}{
				"matchExpressions": []interface{}{map[string]interface{}{"key": "app", "operator": "In", "values": []interface{}{"web"}}},
			}},
		}),
		// Another namespace must not leak in.
		obj("v1", "Pod", "other", "stranger", "x1", map[string]interface{}{"metadata": map[string]interface{}{"labels": map[string]interface{}{"app": "web"}}}),
	}}
}

func hasEdge(g *Graph, src, rel, dst string) bool {
	for _, e := range g.Edges {
		if e.Source == src && string(e.Relation) == rel && e.Target == dst {
			return true
		}
	}
	return false
}

func nodeByID(g *Graph, id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func newTestService(src Source) *Service {
	return NewService(func(string) (Source, error) { return src, nil }, time.Minute)
}

func TestNamespaceGraphRelations(t *testing.T) {
	g, err := newTestService(fixture()).NamespaceGraph(context.Background(), "c", "ns", Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := [][3]string{
		{"Deployment/ns/web", "owns", "ReplicaSet/ns/web-abc"},
		{"ReplicaSet/ns/web-abc", "owns", "Pod/ns/web-abc-1"},
		{"Service/ns/web", "selects", "Pod/ns/web-abc-1"},
		{"Service/ns/web", "exposes", "Deployment/ns/web"},
		{"Ingress/ns/web", "routes", "Service/ns/web"},
		{"Ingress/ns/web", "tls", "Secret/ns/web-tls"},
		{"HTTPRoute/ns/web-route", "routes", "Service/ns/web"},
		{"Gateway/ns/gw", "attaches", "HTTPRoute/ns/web-route"},
		{"Deployment/ns/web", "mounts", "ConfigMap/ns/web-config"},
		{"Deployment/ns/web", "mounts", "PersistentVolumeClaim/ns/web-data"},
		{"Deployment/ns/web", "env", "Secret/ns/web-creds"},
		{"Deployment/ns/web", "env", "Secret/ns/gone"},
		{"Deployment/ns/web", "runs-as", "ServiceAccount/ns/web-sa"},
		{"PersistentVolumeClaim/ns/web-data", "binds", "PersistentVolume//pv-1"},
		{"PersistentVolume//pv-1", "class", "StorageClass//fast"},
		{"HorizontalPodAutoscaler/ns/web", "scales", "Deployment/ns/web"},
		{"PodDisruptionBudget/ns/web", "protects", "Pod/ns/web-abc-2"},
		{"NetworkPolicy/ns/deny", "applies-to", "Pod/ns/web-abc-1"},
		{"Pod/ns/web-abc-1", "runs-on", "Node//node-1"},
	}
	for _, w := range want {
		if !hasEdge(g, w[0], w[1], w[2]) {
			t.Errorf("missing edge %v", w)
		}
	}
	// Pods don't repeat their workload's template refs.
	if hasEdge(g, "Pod/ns/web-abc-1", "mounts", "ConfigMap/ns/web-config") {
		t.Error("owned pod should not carry template refs")
	}
	if n := nodeByID(g, "Secret/ns/gone"); n == nil || !n.Missing {
		t.Errorf("dangling secret should be a missing placeholder: %+v", n)
	}
	if nodeByID(g, "ReplicaSet/ns/web-old") != nil {
		t.Error("idle ReplicaSet should be hidden by default")
	}
	if nodeByID(g, "Pod/other/stranger") != nil {
		t.Error("other namespace leaked")
	}
	if n := nodeByID(g, "Pod/ns/web-abc-1"); n.Owner != "ReplicaSet/ns/web-abc" || n.Health != HealthHealthy {
		t.Errorf("pod node: %+v", n)
	}
	if n := nodeByID(g, "Deployment/ns/web"); n.Health != HealthWarning || n.Status != "1/2 ready" {
		t.Errorf("deployment health: %+v", n)
	}
	if n := nodeByID(g, "Pod/ns/web-abc-2"); n.Health != HealthWarning {
		t.Errorf("pending pod health: %+v", n)
	}
}

func TestIncludeIdleAndKindsFilter(t *testing.T) {
	s := newTestService(fixture())
	g, _ := s.NamespaceGraph(context.Background(), "c", "ns", Filters{IncludeIdle: true})
	if nodeByID(g, "ReplicaSet/ns/web-old") == nil {
		t.Error("IncludeIdle should keep the old ReplicaSet")
	}
	g, _ = s.NamespaceGraph(context.Background(), "c", "ns", Filters{Kinds: []string{"Service", "Ingress"}})
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Errorf("kind filter: %d nodes %d edges", len(g.Nodes), len(g.Edges))
	}
}

func TestNeighbors(t *testing.T) {
	s := newTestService(fixture())
	ctx := context.Background()
	g, err := s.Neighbors(ctx, "c", Ref{Kind: "Service", Namespace: "ns", Name: "web"}, "", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if g.Focus != "Service/ns/web" {
		t.Errorf("focus %q", g.Focus)
	}
	for _, id := range []string{"Ingress/ns/web", "HTTPRoute/ns/web-route", "Deployment/ns/web", "Pod/ns/web-abc-1"} {
		if nodeByID(g, id) == nil {
			t.Errorf("depth 1 should include %s", id)
		}
	}
	if nodeByID(g, "ConfigMap/ns/web-config") != nil || nodeByID(g, "Gateway/ns/gw") != nil {
		t.Error("depth 1 went too far")
	}
	g, _ = s.Neighbors(ctx, "c", Ref{Kind: "Service", Namespace: "ns", Name: "web"}, "", 2, false)
	if nodeByID(g, "ConfigMap/ns/web-config") == nil || nodeByID(g, "Gateway/ns/gw") == nil {
		t.Error("depth 2 should reach config and gateway")
	}
	// A Node is reached but not fanned out through.
	g, _ = s.Neighbors(ctx, "c", Ref{Kind: "Pod", Namespace: "ns", Name: "web-abc-1"}, "", 2, false)
	if nodeByID(g, "Node//node-1") == nil {
		t.Error("pod should reach its node")
	}
	// Cluster-scoped ref needs namespace context.
	if _, err := s.Neighbors(ctx, "c", Ref{Kind: "Node", Name: "node-1"}, "", 1, false); err == nil {
		t.Error("expected error without namespace")
	}
	g, _ = s.Neighbors(ctx, "c", Ref{Kind: "Node", Name: "node-1"}, "ns", 1, false)
	if len(g.Nodes) != 3 {
		t.Errorf("node neighbors: %d", len(g.Nodes))
	}
	g, _ = s.Neighbors(ctx, "c", Ref{Kind: "Deployment", Namespace: "ns", Name: "nope"}, "", 1, false)
	if len(g.Nodes) != 1 || !g.Nodes[0].Missing {
		t.Errorf("unknown ref: %+v", g.Nodes)
	}
}

func TestPartialAccessAndCaching(t *testing.T) {
	src := fixture()
	src.errs = map[string]error{
		"Secret":    apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", errors.New("no")),
		"HTTPRoute": apierrors.NewNotFound(schema.GroupResource{Resource: "httproutes"}, ""),
	}
	s := newTestService(src)
	g, err := s.NamespaceGraph(context.Background(), "c", "ns", Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Warnings) != 1 || g.Warnings[0] != "Secret: access denied" {
		t.Errorf("warnings: %v", g.Warnings)
	}
	if n := nodeByID(g, "Secret/ns/web-creds"); n == nil || n.Missing {
		t.Errorf("unreadable secret must not be claimed missing: %+v", n)
	}
	before := src.lists
	_, _ = s.Neighbors(context.Background(), "c", Ref{Kind: "Service", Namespace: "ns", Name: "web"}, "", 1, false)
	if src.lists != before {
		t.Error("second query within TTL should reuse the snapshot")
	}
	s.Invalidate("c")
	_, _ = s.NamespaceGraph(context.Background(), "c", "ns", Filters{})
	if src.lists == before {
		t.Error("Invalidate should force a reload")
	}
}

func TestTruncationKeepsWorkloads(t *testing.T) {
	g, _ := newTestService(fixture()).NamespaceGraph(context.Background(), "c", "ns", Filters{MaxNodes: 3})
	if !g.Truncated || len(g.Nodes) != 3 {
		t.Fatalf("truncation: %v %d", g.Truncated, len(g.Nodes))
	}
	for _, n := range g.Nodes {
		if n.Kind == "Pod" || n.Kind == "ConfigMap" {
			t.Errorf("truncation kept low-rank %s", n.ID)
		}
	}
}

func TestAllKindsFailing(t *testing.T) {
	src := &fakeSource{errs: map[string]error{}}
	for _, k := range namespacedKinds {
		src.errs[k.Kind] = apierrors.NewForbidden(schema.GroupResource{Resource: k.Resource}, "", errors.New("no"))
	}
	if _, err := newTestService(src).NamespaceGraph(context.Background(), "c", "ns", Filters{}); err == nil {
		t.Error("expected an error when nothing is readable")
	}
}
