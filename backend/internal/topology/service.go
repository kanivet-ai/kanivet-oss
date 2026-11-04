package topology

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
)

func fromUnstructured(m map[string]interface{}, out interface{}) error {
	return runtime.DefaultUnstructuredConverter.FromUnstructured(m, out)
}

// SourceFunc returns the Source for a cluster.
type SourceFunc func(cluster string) (Source, error)

// Service answers topology queries. Namespace snapshots are cached for TTL
// so expanding node after node, and polling for live updates, costs one
// read of the namespace per TTL window, shared by every caller.
type Service struct {
	source SourceFunc
	ttl    time.Duration
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	once  sync.Once
	at    time.Time
	graph *fullGraph
	err   error
}

// DefaultTTL is how long a namespace snapshot is reused.
const DefaultTTL = 4 * time.Second

func NewService(source SourceFunc, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Service{source: source, ttl: ttl, now: time.Now, entries: map[string]*cacheEntry{}}
}

func (s *Service) graphFor(ctx context.Context, cluster, namespace string) (*fullGraph, error) {
	key := cluster + "\x00" + namespace
	s.mu.Lock()
	e := s.entries[key]
	if e != nil && e.graph == nil && e.err == nil {
		// in flight: wait on it below
	} else if e == nil || s.now().Sub(e.at) > s.ttl || e.err != nil {
		e = &cacheEntry{}
		s.entries[key] = e
	}
	// Drop stale entries so the map doesn't grow without bound.
	for k, old := range s.entries {
		if k != key && !old.at.IsZero() && s.now().Sub(old.at) > 10*s.ttl {
			delete(s.entries, k)
		}
	}
	s.mu.Unlock()
	e.once.Do(func() {
		var graph *fullGraph
		src, err := s.source(cluster)
		if err == nil {
			var snap *Snapshot
			snap, err = LoadSnapshot(ctx, src, namespace)
			if err == nil {
				graph = buildGraph(snap)
			}
		}
		s.mu.Lock()
		e.graph, e.err, e.at = graph, err, s.now()
		s.mu.Unlock()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return e.graph, e.err
}

// Invalidate drops cached snapshots for a cluster ("" for all).
func (s *Service) Invalidate(cluster string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.entries {
		if cluster == "" || len(k) > len(cluster) && k[:len(cluster)+1] == cluster+"\x00" {
			delete(s.entries, k)
		}
	}
}

// Neighbors returns the resources within depth hops of ref (in either edge
// direction), and the edges between them. depth is clamped to [1, 6].
//
// For a cluster-scoped ref (Node, PersistentVolume, StorageClass), namespace
// names the namespace whose resources to relate it to; ref.Namespace is
// used otherwise.
func (s *Service) Neighbors(ctx context.Context, cluster string, ref Ref, namespace string, depth int, includeIdle bool) (*Graph, error) {
	if ref.Namespace != "" {
		namespace = ref.Namespace
	}
	if namespace == "" {
		return nil, fmt.Errorf("a namespace is required to relate %s %q", ref.Kind, ref.Name)
	}
	g, err := s.graphFor(ctx, cluster, namespace)
	if err != nil {
		return nil, err
	}
	return g.neighbors(ref, depth, includeIdle), nil
}

// NamespaceGraph returns every resource in namespace and how they relate.
func (s *Service) NamespaceGraph(ctx context.Context, cluster, namespace string, f Filters) (*Graph, error) {
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	g, err := s.graphFor(ctx, cluster, namespace)
	if err != nil {
		return nil, err
	}
	return g.filtered(f), nil
}

// idle reports a ReplicaSet scaled to zero: an old rollout revision.
func (g *fullGraph) idle(id string) bool {
	n := g.nodes[id]
	if n == nil || n.Kind != "ReplicaSet" {
		return false
	}
	u := g.snap.objects[id]
	return u != nil && i64(u.Object, "spec", "replicas") == 0 && i64(u.Object, "status", "replicas") == 0
}

func (g *fullGraph) neighbors(ref Ref, depth int, includeIdle bool) *Graph {
	if depth < 1 {
		depth = 1
	}
	if depth > 6 {
		depth = 6
	}
	// The cached graph is shared, so never add to it here.
	if spec, ok := specByKind[ref.Kind]; ok {
		if !spec.Namespaced {
			ref.Namespace = ""
		}
		if ref.Group == "" && ref.Version == "" {
			ref.Group, ref.Version = spec.Group, spec.Version
		}
	}
	start := ref.ID()
	if g.nodes[start] == nil {
		return &Graph{
			Nodes:    []Node{{ID: start, Ref: ref, Health: HealthUnknown, Missing: true}},
			Edges:    []Edge{},
			Focus:    start,
			Warnings: g.snap.warnings,
		}
	}
	keep := map[string]bool{start: true}
	frontier := []string{start}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, id := range frontier {
			for _, eid := range g.adj[id] {
				e := g.edges[eid]
				other := e.Target
				if other == id {
					other = e.Source
				}
				if keep[other] || (!includeIdle && g.idle(other)) {
					continue
				}
				// Don't fan out through a Node: every pod on it would
				// come along, which isn't what "related" means.
				keep[other] = true
				if g.nodes[other].Kind != "Node" && g.nodes[other].Kind != "StorageClass" {
					next = append(next, other)
				}
			}
		}
		frontier = next
	}
	out := g.subgraph(keep, DefaultMaxNodes)
	out.Focus = start
	return out
}

func (g *fullGraph) filtered(f Filters) *Graph {
	kinds := map[string]bool{}
	for _, k := range f.Kinds {
		kinds[k] = true
	}
	keep := map[string]bool{}
	for id, n := range g.nodes {
		if len(kinds) > 0 && !kinds[n.Kind] {
			continue
		}
		if !f.IncludeIdle && g.idle(id) {
			continue
		}
		// Cluster-scoped and placeholder nodes only appear when related.
		if n.Namespace == "" || n.Namespace != g.snap.Namespace {
			if len(g.adj[id]) == 0 {
				continue
			}
		}
		keep[id] = true
	}
	max := f.MaxNodes
	if max <= 0 {
		max = DefaultMaxNodes
	}
	return g.subgraph(keep, max)
}

// kindRank orders nodes so truncation drops pods and config before
// workloads and network entry points.
var kindRank = map[string]int{
	"Gateway": 0, "HTTPRoute": 1, "Ingress": 1, "Service": 2,
	"Deployment": 3, "StatefulSet": 3, "DaemonSet": 3, "CronJob": 3,
	"Job": 4, "ReplicaSet": 4, "HorizontalPodAutoscaler": 5, "PodDisruptionBudget": 5,
	"NetworkPolicy": 5, "Pod": 6, "PersistentVolumeClaim": 7, "ConfigMap": 8, "Secret": 8,
	"ServiceAccount": 8, "PersistentVolume": 9, "StorageClass": 9, "Node": 9,
}

func rankOf(kind string) int {
	if r, ok := kindRank[kind]; ok {
		return r
	}
	return 10
}

func (g *fullGraph) subgraph(keep map[string]bool, max int) *Graph {
	ids := make([]string, 0, len(keep))
	for id := range keep {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ri, rj := rankOf(g.nodes[ids[i]].Kind), rankOf(g.nodes[ids[j]].Kind)
		if ri != rj {
			return ri < rj
		}
		return ids[i] < ids[j]
	})
	out := &Graph{Nodes: []Node{}, Edges: []Edge{}, Warnings: g.snap.warnings}
	if len(ids) > max {
		ids, out.Truncated = ids[:max], true
	}
	in := make(map[string]bool, len(ids))
	for _, id := range ids {
		in[id] = true
		out.Nodes = append(out.Nodes, *g.nodes[id])
	}
	for i := range out.Nodes {
		if !in[out.Nodes[i].Owner] {
			out.Nodes[i].Owner = ""
		}
	}
	eids := make([]string, 0)
	for _, id := range ids {
		for _, eid := range g.adj[id] {
			e := g.edges[eid]
			if e.Source == id && in[e.Target] {
				eids = append(eids, eid)
			}
		}
	}
	sort.Strings(eids)
	for _, eid := range eids {
		out.Edges = append(out.Edges, g.edges[eid])
	}
	return out
}
