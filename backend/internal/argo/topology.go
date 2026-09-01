package argo

import (
	"context"
	"sort"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

type TopologyNode struct {
	Group      string         `json:"group"`
	Version    string         `json:"version"`
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace,omitempty"`
	Name       string         `json:"name"`
	UID        string         `json:"uid,omitempty"`
	Phase      string         `json:"phase,omitempty"`
	Health     string         `json:"health,omitempty"`
	SyncStatus string         `json:"syncStatus,omitempty"`
	Ready      string         `json:"ready,omitempty"`
	Message    string         `json:"message,omitempty"`
	Exists     bool           `json:"exists"`
	Children   []TopologyNode `json:"children,omitempty"`
}

const (
	topologyMaxConcurrency = 8
	topologyMaxDepth       = 4
)

type childTarget struct {
	Group   string
	Version string
	Kind    string
}

func childKindsFor(parentKind string) []childTarget {
	switch parentKind {
	case "Deployment", "StatefulSet", "DaemonSet":
		return []childTarget{
			{Group: "apps", Version: "v1", Kind: "ReplicaSet"},
			{Group: "", Version: "v1", Kind: "Pod"},
		}
	case "ReplicaSet":
		return []childTarget{{Group: "", Version: "v1", Kind: "Pod"}}
	case "Job":
		return []childTarget{{Group: "", Version: "v1", Kind: "Pod"}}
	case "CronJob":
		return []childTarget{{Group: "batch", Version: "v1", Kind: "Job"}}
	case "Service":
		return []childTarget{{Group: "", Version: "v1", Kind: "Endpoints"}}
	}
	return nil
}

// inventory is a per-namespace cache of live objects for the kinds we need.
// One entry per kind, indexed by uid (for owner lookups) and by name.
type inventoryEntry struct {
	plural     string
	namespaced bool
	gvr        schema.GroupVersionResource
	target     childTarget
	byOwnerUID map[types.UID][]*unstructured.Unstructured
	byName     map[string]*unstructured.Unstructured
}

type inventory struct {
	namespace string
	entries   map[string]*inventoryEntry // keyed by Kind
}

func newInventory(namespace string) *inventory {
	return &inventory{namespace: namespace, entries: make(map[string]*inventoryEntry)}
}

func (inv *inventory) listChildKinds(rootKinds []string) []childTarget {
	seen := make(map[string]bool)
	out := make([]childTarget, 0, 8)
	var addClosure func(kind string)
	addClosure = func(kind string) {
		for _, c := range childKindsFor(kind) {
			key := c.Group + "/" + c.Version + "/" + c.Kind
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, c)
			addClosure(c.Kind)
		}
	}
	for _, k := range rootKinds {
		addClosure(k)
	}
	return out
}

func (inv *inventory) prefetch(ctx context.Context, dyn dynamic.Interface, resolver ResourceResolver, targets []childTarget) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, topologyMaxConcurrency)
	for _, t := range targets {
		wg.Add(1)
		go func(c childTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			plural, namespaced, err := resolver(c.Group, c.Version, c.Kind)
			if err != nil || plural == "" {
				return
			}
			gvr := schema.GroupVersionResource{Group: c.Group, Version: c.Version, Resource: plural}
			lister := dyn.Resource(gvr)
			var list *unstructured.UnstructuredList
			if namespaced && inv.namespace != "" {
				list, err = lister.Namespace(inv.namespace).List(ctx, metav1.ListOptions{})
			} else {
				list, err = lister.List(ctx, metav1.ListOptions{})
			}
			if err != nil || list == nil {
				return
			}
			byOwner := make(map[types.UID][]*unstructured.Unstructured)
			byName := make(map[string]*unstructured.Unstructured)
			for i := range list.Items {
				item := &list.Items[i]
				byName[item.GetName()] = item
				for _, owner := range item.GetOwnerReferences() {
					byOwner[owner.UID] = append(byOwner[owner.UID], item)
				}
			}
			mu.Lock()
			inv.entries[c.Kind] = &inventoryEntry{
				plural:     plural,
				namespaced: namespaced,
				gvr:        gvr,
				target:     c,
				byOwnerUID: byOwner,
				byName:     byName,
			}
			mu.Unlock()
		}(t)
	}
	wg.Wait()
}

// findRoot fetches an Application-managed root by Kind+name from the inventory if available,
// otherwise issues a single Get against the cluster.
func (inv *inventory) findOrFetchRoot(ctx context.Context, dyn dynamic.Interface, resolver ResourceResolver, kind, group, version, namespace, name string) *unstructured.Unstructured {
	if entry, ok := inv.entries[kind]; ok {
		if u, ok := entry.byName[name]; ok {
			return u
		}
	}
	if resolver == nil {
		return nil
	}
	plural, namespaced, err := resolver(group, version, kind)
	if err != nil || plural == "" {
		return nil
	}
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: plural}
	var u *unstructured.Unstructured
	if namespaced && namespace != "" {
		u, err = dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		u, err = dyn.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil
	}
	return u
}

func BuildTopology(ctx context.Context, dyn dynamic.Interface, resolver ResourceResolver, app *AppSummary, root unstructured.Unstructured) ([]TopologyNode, error) {
	if app == nil || dyn == nil {
		return nil, nil
	}

	rootEntries := make([]TopologyNode, 0, len(app.Resources))
	for _, r := range app.Resources {
		n := TopologyNode{
			Group:      r.Group,
			Version:    r.Version,
			Kind:       r.Kind,
			Namespace:  r.Namespace,
			Name:       r.Name,
			SyncStatus: r.SyncStatus,
			Health:     r.Health,
			Message:    r.Message,
			Exists:     r.Exists,
			Phase:      r.Status,
		}
		if n.Namespace == "" {
			n.Namespace = app.DestNamespace
		}
		rootEntries = append(rootEntries, n)
	}

	if resolver == nil {
		return rootEntries, nil
	}

	rootKinds := make([]string, 0, len(rootEntries))
	rootKindSet := make(map[string]bool)
	for _, n := range rootEntries {
		if !rootKindSet[n.Kind] {
			rootKindSet[n.Kind] = true
			rootKinds = append(rootKinds, n.Kind)
		}
	}

	inv := newInventory(app.DestNamespace)
	inv.prefetch(ctx, dyn, resolver, inv.listChildKinds(rootKinds))

	var wg sync.WaitGroup
	sem := make(chan struct{}, topologyMaxConcurrency)
	for i := range rootEntries {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			node := &rootEntries[idx]
			live := inv.findOrFetchRoot(ctx, dyn, resolver, node.Kind, node.Group, node.Version, node.Namespace, node.Name)
			if live == nil {
				node.Exists = false
				return
			}
			node.Exists = true
			node.UID = string(live.GetUID())
			enrichFromLive(node, live)
			children := expandFromInventory(inv, *node, 0)
			if len(children) > 0 {
				node.Children = children
			}
		}(i)
	}
	wg.Wait()

	sort.Slice(rootEntries, func(i, j int) bool {
		if rootEntries[i].Kind != rootEntries[j].Kind {
			return rootEntries[i].Kind < rootEntries[j].Kind
		}
		if rootEntries[i].Namespace != rootEntries[j].Namespace {
			return rootEntries[i].Namespace < rootEntries[j].Namespace
		}
		return rootEntries[i].Name < rootEntries[j].Name
	})
	return rootEntries, nil
}

func expandFromInventory(inv *inventory, parent TopologyNode, depth int) []TopologyNode {
	if depth >= topologyMaxDepth {
		return nil
	}
	targets := childKindsFor(parent.Kind)
	if len(targets) == 0 {
		return nil
	}
	parentUID := types.UID(parent.UID)
	out := make([]TopologyNode, 0)
	for _, c := range targets {
		entry, ok := inv.entries[c.Kind]
		if !ok {
			continue
		}
		var matches []*unstructured.Unstructured
		if parentUID != "" {
			matches = append(matches, entry.byOwnerUID[parentUID]...)
		}
		if parent.Kind == "Service" && c.Kind == "Endpoints" {
			if ep, ok := entry.byName[parent.Name]; ok {
				matches = append(matches, ep)
			}
		}
		for _, item := range matches {
			node := nodeFromUnstructured(item, c.Group, c.Version, c.Kind)
			children := expandFromInventory(inv, node, depth+1)
			if len(children) > 0 {
				node.Children = children
			}
			out = append(out, node)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func nodeFromUnstructured(u *unstructured.Unstructured, group, version, kind string) TopologyNode {
	node := TopologyNode{
		Group:     group,
		Version:   version,
		Kind:      kind,
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		UID:       string(u.GetUID()),
		Exists:    true,
	}
	enrichFromLive(&node, u)
	return node
}

func enrichFromLive(node *TopologyNode, u *unstructured.Unstructured) {
	status, _, _ := unstructured.NestedMap(u.Object, "status")
	if status == nil {
		return
	}
	if phase, ok, _ := unstructured.NestedString(status, "phase"); ok && phase != "" {
		node.Phase = phase
	}
	if node.Kind == "Pod" {
		ready, total := podReadiness(status)
		if total > 0 {
			node.Ready = formatRatio(ready, total)
		}
		if node.Phase == "" {
			node.Phase = "Unknown"
		}
		node.Health = podHealth(node.Phase, ready, total)
		if msg, ok, _ := unstructured.NestedString(status, "message"); ok {
			node.Message = msg
		}
		return
	}
	switch node.Kind {
	case "Deployment", "ReplicaSet", "StatefulSet":
		ready, _ := nestedInt(status, "readyReplicas")
		desired, _ := nestedInt(status, "replicas")
		if desired == 0 {
			if spec, ok, _ := unstructured.NestedMap(u.Object, "spec"); ok {
				if v, ok := spec["replicas"]; ok {
					switch t := v.(type) {
					case int64:
						desired = int32(t)
					case float64:
						desired = int32(t)
					}
				}
			}
		}
		if desired > 0 {
			node.Ready = formatRatio(ready, desired)
		}
		node.Health = workloadHealth(ready, desired)
	case "DaemonSet":
		ready, _ := nestedInt(status, "numberReady")
		desired, _ := nestedInt(status, "desiredNumberScheduled")
		if desired > 0 {
			node.Ready = formatRatio(ready, desired)
		}
		node.Health = workloadHealth(ready, desired)
	case "Job":
		succeeded, _ := nestedInt(status, "succeeded")
		failed, _ := nestedInt(status, "failed")
		if failed > 0 {
			node.Health = "Degraded"
		} else if succeeded > 0 {
			node.Health = "Healthy"
		} else {
			node.Health = "Progressing"
		}
	}
}

func nestedInt(m map[string]interface{}, key string) (int32, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int64:
		return int32(t), true
	case int32:
		return t, true
	case float64:
		return int32(t), true
	}
	return 0, false
}

func podReadiness(status map[string]interface{}) (int32, int32) {
	conditions, _, _ := unstructured.NestedSlice(status, "containerStatuses")
	total := int32(len(conditions))
	ready := int32(0)
	for _, c := range conditions {
		if cm, ok := c.(map[string]interface{}); ok {
			if b, _, _ := unstructured.NestedBool(cm, "ready"); b {
				ready++
			}
		}
	}
	return ready, total
}

func podHealth(phase string, ready, total int32) string {
	switch strings.ToLower(phase) {
	case "running":
		if ready == total && total > 0 {
			return "Healthy"
		}
		return "Progressing"
	case "succeeded":
		return "Healthy"
	case "pending":
		return "Progressing"
	case "failed":
		return "Degraded"
	case "unknown":
		return "Unknown"
	}
	return ""
}

func workloadHealth(ready, desired int32) string {
	if desired == 0 {
		return "Suspended"
	}
	if ready == desired {
		return "Healthy"
	}
	if ready == 0 {
		return "Degraded"
	}
	return "Progressing"
}

func formatRatio(a, b int32) string {
	return strings.Join([]string{i32String(a), i32String(b)}, "/")
}

func i32String(v int32) string {
	if v == 0 {
		return "0"
	}
	digits := make([]byte, 0, 10)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
