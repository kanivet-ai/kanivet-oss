package topology

import (
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// fullGraph is every node and edge derivable from a Snapshot.
type fullGraph struct {
	snap  *Snapshot
	nodes map[string]*Node
	edges map[string]Edge
	adj   map[string][]string // node ID -> edge IDs touching it
}

func (g *fullGraph) node(u *unstructured.Unstructured) string {
	r := refOf(u)
	id := r.ID()
	if _, ok := g.nodes[id]; ok {
		return id
	}
	h, st := healthOf(u)
	n := &Node{ID: id, Ref: r, UID: string(u.GetUID()), Health: h, Status: st}
	if c := metav1.GetControllerOf(u); c != nil {
		if owner := g.snap.byUID[string(c.UID)]; owner != nil {
			n.Owner = refOf(owner).ID()
		}
	}
	g.nodes[id] = n
	return id
}

// ref returns the node ID for (kind, ns, name), adding a placeholder when the
// object isn't in the snapshot.
func (g *fullGraph) ref(kind, ns, name string) string {
	spec, known := specByKind[kind]
	if known && !spec.Namespaced {
		ns = ""
	}
	r := Ref{Kind: kind, Namespace: ns, Name: name}
	if known {
		r.Group, r.Version = spec.Group, spec.Version
	}
	id := r.ID()
	if _, ok := g.nodes[id]; ok {
		return id
	}
	if u := g.snap.objects[id]; u != nil {
		return g.node(u)
	}
	// Absent from the snapshot. It is "missing" only when we would have
	// listed it: a namespaced kind we read, in our namespace, with no warning.
	missing := known && (!spec.Namespaced || ns == g.snap.Namespace) && !g.snap.warned(kind)
	if known && !spec.Namespaced {
		missing = false // cluster-scoped Get may be forbidden; don't claim absence
	}
	g.nodes[id] = &Node{ID: id, Ref: r, Health: HealthUnknown, Missing: missing}
	return id
}

func (s *Snapshot) warned(kind string) bool {
	for _, w := range s.warnings {
		if strings.HasPrefix(w, kind+":") {
			return true
		}
	}
	return false
}

func (g *fullGraph) link(src, dst string, rel Relation) {
	if src == dst {
		return
	}
	id := src + "|" + string(rel) + "|" + dst
	if _, ok := g.edges[id]; ok {
		return
	}
	g.edges[id] = Edge{ID: id, Source: src, Target: dst, Relation: rel}
	g.adj[src] = append(g.adj[src], id)
	g.adj[dst] = append(g.adj[dst], id)
}

// buildGraph derives every relationship in the snapshot.
func buildGraph(snap *Snapshot) *fullGraph {
	g := &fullGraph{snap: snap, nodes: map[string]*Node{}, edges: map[string]Edge{}, adj: map[string][]string{}}
	ids := make([]string, 0, len(snap.objects))
	for id := range snap.objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g.node(snap.objects[id])
	}
	ns := snap.Namespace

	// Ownership.
	for _, id := range ids {
		u := snap.objects[id]
		for _, o := range u.GetOwnerReferences() {
			if owner := snap.byUID[string(o.UID)]; owner != nil {
				g.link(g.node(owner), id, RelOwns)
			}
		}
	}

	// Pod-template references, from the top of each ownership chain only, so
	// a Deployment's ConfigMap hangs off the Deployment, not every pod.
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod"} {
		for _, u := range snap.byKind[kind] {
			if g.nodes[refOf(u).ID()].Owner != "" {
				continue
			}
			if spec := podSpecOf(u); spec != nil {
				g.podSpecRefs(refOf(u).ID(), u.GetNamespace(), spec)
			}
		}
	}

	pods := snap.byKind["Pod"]
	for _, p := range pods {
		pid := refOf(p).ID()
		if n := str(p.Object, "spec", "nodeName"); n != "" {
			g.link(pid, g.ref("Node", "", n), RelRunsOn)
		}
		// Owned pods skip template refs, but their claims are per pod
		// (StatefulSet volumeClaimTemplates), so link those directly.
		if g.nodes[pid].Owner != "" {
			vols, _, _ := unstructured.NestedSlice(p.Object, "spec", "volumes")
			for _, v := range vols {
				vm, _ := v.(map[string]interface{})
				if n := str(vm, "persistentVolumeClaim", "claimName"); n != "" {
					g.link(pid, g.ref("PersistentVolumeClaim", p.GetNamespace(), n), RelMounts)
				}
			}
		}
	}

	// Services select pods, and through them expose the owning workload.
	for _, svc := range snap.byKind["Service"] {
		sel, _, _ := unstructured.NestedStringMap(svc.Object, "spec", "selector")
		if len(sel) == 0 {
			continue
		}
		selector := labels.SelectorFromSet(sel)
		sid := refOf(svc).ID()
		for _, p := range pods {
			if !selector.Matches(labels.Set(p.GetLabels())) {
				continue
			}
			pid := refOf(p).ID()
			g.link(sid, pid, RelSelects)
			if top := g.topOwner(pid); top != pid {
				g.link(sid, top, RelExposes)
			}
		}
	}

	for _, kind := range []string{"PodDisruptionBudget", "NetworkPolicy"} {
		path := []string{"spec", "selector"}
		rel := RelProtects
		if kind == "NetworkPolicy" {
			path, rel = []string{"spec", "podSelector"}, RelPolicy
		}
		for _, obj := range snap.byKind[kind] {
			m, ok, _ := unstructured.NestedMap(obj.Object, path...)
			if !ok {
				continue
			}
			var ls metav1.LabelSelector
			if err := fromUnstructured(m, &ls); err != nil {
				continue
			}
			selector, err := metav1.LabelSelectorAsSelector(&ls)
			if err != nil || (kind == "PodDisruptionBudget" && selector.Empty()) {
				continue
			}
			oid := refOf(obj).ID()
			for _, p := range pods {
				if selector.Matches(labels.Set(p.GetLabels())) {
					g.link(oid, refOf(p).ID(), rel)
				}
			}
		}
	}

	for _, ing := range snap.byKind["Ingress"] {
		iid := refOf(ing).ID()
		if n := str(ing.Object, "spec", "defaultBackend", "service", "name"); n != "" {
			g.link(iid, g.ref("Service", ns, n), RelRoutes)
		}
		rules, _, _ := unstructured.NestedSlice(ing.Object, "spec", "rules")
		for _, r := range rules {
			rm, _ := r.(map[string]interface{})
			paths, _, _ := unstructured.NestedSlice(rm, "http", "paths")
			for _, p := range paths {
				pm, _ := p.(map[string]interface{})
				if n := str(pm, "backend", "service", "name"); n != "" {
					g.link(iid, g.ref("Service", ns, n), RelRoutes)
				}
			}
		}
		tls, _, _ := unstructured.NestedSlice(ing.Object, "spec", "tls")
		for _, t := range tls {
			tm, _ := t.(map[string]interface{})
			if n, _ := tm["secretName"].(string); n != "" {
				g.link(iid, g.ref("Secret", ns, n), RelTLS)
			}
		}
	}

	for _, rt := range snap.byKind["HTTPRoute"] {
		rid := refOf(rt).ID()
		parents, _, _ := unstructured.NestedSlice(rt.Object, "spec", "parentRefs")
		for _, p := range parents {
			pm, _ := p.(map[string]interface{})
			if k, _ := pm["kind"].(string); k != "" && k != "Gateway" {
				continue
			}
			name, _ := pm["name"].(string)
			pns, _ := pm["namespace"].(string)
			if pns == "" {
				pns = ns
			}
			if name != "" {
				g.link(g.ref("Gateway", pns, name), rid, RelAttaches)
			}
		}
		rules, _, _ := unstructured.NestedSlice(rt.Object, "spec", "rules")
		for _, r := range rules {
			rm, _ := r.(map[string]interface{})
			backends, _, _ := unstructured.NestedSlice(rm, "backendRefs")
			for _, b := range backends {
				bm, _ := b.(map[string]interface{})
				if k, _ := bm["kind"].(string); k != "" && k != "Service" {
					continue
				}
				name, _ := bm["name"].(string)
				bns, _ := bm["namespace"].(string)
				if bns == "" {
					bns = ns
				}
				if name != "" {
					g.link(rid, g.ref("Service", bns, name), RelRoutes)
				}
			}
		}
	}

	for _, c := range snap.byKind["PersistentVolumeClaim"] {
		cid := refOf(c).ID()
		if v := str(c.Object, "spec", "volumeName"); v != "" {
			g.link(cid, g.ref("PersistentVolume", "", v), RelBinds)
		}
		if sc := str(c.Object, "spec", "storageClassName"); sc != "" {
			g.link(cid, g.ref("StorageClass", "", sc), RelClass)
		}
	}
	for _, pv := range snap.byKind["PersistentVolume"] {
		if sc := str(pv.Object, "spec", "storageClassName"); sc != "" {
			g.link(refOf(pv).ID(), g.ref("StorageClass", "", sc), RelClass)
		}
	}

	for _, kind := range []string{"HorizontalPodAutoscaler"} {
		for _, h := range snap.byKind[kind] {
			k := str(h.Object, "spec", "scaleTargetRef", "kind")
			n := str(h.Object, "spec", "scaleTargetRef", "name")
			if k == "" || n == "" {
				continue
			}
			tid := g.ref(k, ns, n)
			if av := str(h.Object, "spec", "scaleTargetRef", "apiVersion"); av != "" {
				if gv, err := schema.ParseGroupVersion(av); err == nil && g.nodes[tid].Group == "" && g.nodes[tid].Version == "" {
					g.nodes[tid].Group, g.nodes[tid].Version = gv.Group, gv.Version
				}
			}
			g.link(refOf(h).ID(), tid, RelScales)
		}
	}
	return g
}

// topOwner follows controller owners to the top of the chain.
func (g *fullGraph) topOwner(id string) string {
	seen := map[string]bool{}
	for {
		n := g.nodes[id]
		if n == nil || n.Owner == "" || seen[id] {
			return id
		}
		seen[id] = true
		id = n.Owner
	}
}

func podSpecOf(u *unstructured.Unstructured) map[string]interface{} {
	var path []string
	switch u.GetKind() {
	case "Pod":
		path = []string{"spec"}
	case "CronJob":
		path = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	default:
		path = []string{"spec", "template", "spec"}
	}
	m, ok, _ := unstructured.NestedMap(u.Object, path...)
	if !ok {
		return nil
	}
	return m
}

// podSpecRefs links a pod spec's ConfigMaps, Secrets, PVCs and
// ServiceAccount to owner.
func (g *fullGraph) podSpecRefs(owner, ns string, spec map[string]interface{}) {
	if sa := str(spec, "serviceAccountName"); sa != "" {
		g.link(owner, g.ref("ServiceAccount", ns, sa), RelRunsAs)
	}
	ips, _, _ := unstructured.NestedSlice(spec, "imagePullSecrets")
	for _, s := range ips {
		if m, ok := s.(map[string]interface{}); ok {
			if n, _ := m["name"].(string); n != "" {
				g.link(owner, g.ref("Secret", ns, n), RelImagePull)
			}
		}
	}
	vols, _, _ := unstructured.NestedSlice(spec, "volumes")
	for _, v := range vols {
		vm, _ := v.(map[string]interface{})
		if n := str(vm, "configMap", "name"); n != "" {
			g.link(owner, g.ref("ConfigMap", ns, n), RelMounts)
		}
		if n := str(vm, "secret", "secretName"); n != "" {
			g.link(owner, g.ref("Secret", ns, n), RelMounts)
		}
		if n := str(vm, "persistentVolumeClaim", "claimName"); n != "" {
			g.link(owner, g.ref("PersistentVolumeClaim", ns, n), RelMounts)
		}
		sources, _, _ := unstructured.NestedSlice(vm, "projected", "sources")
		for _, s := range sources {
			sm, _ := s.(map[string]interface{})
			if n := str(sm, "configMap", "name"); n != "" {
				g.link(owner, g.ref("ConfigMap", ns, n), RelMounts)
			}
			if n := str(sm, "secret", "name"); n != "" {
				g.link(owner, g.ref("Secret", ns, n), RelMounts)
			}
		}
	}
	for _, field := range []string{"initContainers", "containers", "ephemeralContainers"} {
		cs, _, _ := unstructured.NestedSlice(spec, field)
		for _, c := range cs {
			cm, _ := c.(map[string]interface{})
			envFrom, _, _ := unstructured.NestedSlice(cm, "envFrom")
			for _, e := range envFrom {
				em, _ := e.(map[string]interface{})
				if n := str(em, "configMapRef", "name"); n != "" {
					g.link(owner, g.ref("ConfigMap", ns, n), RelEnv)
				}
				if n := str(em, "secretRef", "name"); n != "" {
					g.link(owner, g.ref("Secret", ns, n), RelEnv)
				}
			}
			env, _, _ := unstructured.NestedSlice(cm, "env")
			for _, e := range env {
				em, _ := e.(map[string]interface{})
				if n := str(em, "valueFrom", "configMapKeyRef", "name"); n != "" {
					g.link(owner, g.ref("ConfigMap", ns, n), RelEnv)
				}
				if n := str(em, "valueFrom", "secretKeyRef", "name"); n != "" {
					g.link(owner, g.ref("Secret", ns, n), RelEnv)
				}
			}
		}
	}
}
