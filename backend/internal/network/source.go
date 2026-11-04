package network

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodLister is the shared pod cache (podcache.Cache), fed by the watcher's
// pods watch.
type PodLister interface {
	List(ctx context.Context, cluster string) ([]*corev1.Pod, error)
}

// ClientSource builds clientsets (k8s.Interface satisfies it).
type ClientSource interface {
	GetClientForCluster(cluster string) (kubernetes.Interface, error)
}

// SnapshotSource returns the cluster state to analyse.
type SnapshotSource interface {
	Snapshot(ctx context.Context, cluster string) (*Snapshot, error)
}

// snapshotTTL is how long listed NetworkPolicies, Services, EndpointSlices
// and Namespaces are reused. They are small, change rarely, and a path check
// is often repeated while someone edits a policy, so the TTL is short.
const snapshotTTL = 10 * time.Second

// LiveSource reads pods from the shared pod cache (no pod LIST) and lists
// the other, small, kinds with a short-lived per-cluster cache. A kind the
// user may not list is treated as empty and named in the snapshot's warnings.
type LiveSource struct {
	clients ClientSource
	pods    PodLister

	mu    sync.Mutex
	cache map[string]*cachedLists
}

type cachedLists struct {
	mu       sync.Mutex
	at       time.Time
	ns       []*corev1.Namespace
	svcs     []*corev1.Service
	slices   []*discoveryv1.EndpointSlice
	policies []*netv1.NetworkPolicy
	warnings []string
}

func NewLiveSource(clients ClientSource, pods PodLister) *LiveSource {
	return &LiveSource{clients: clients, pods: pods, cache: map[string]*cachedLists{}}
}

// Invalidate drops a cluster's cached lists, so the next snapshot re-reads.
func (l *LiveSource) Invalidate(cluster string) {
	l.mu.Lock()
	delete(l.cache, cluster)
	l.mu.Unlock()
}

func (l *LiveSource) Snapshot(ctx context.Context, cluster string) (*Snapshot, error) {
	cs, err := l.clients.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pods, err := l.pods.List(pctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("pods: %w", err)
	}
	l.mu.Lock()
	c := l.cache[cluster]
	if c == nil {
		c = &cachedLists{}
		l.cache[cluster] = c
	}
	l.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > snapshotTTL {
		if err := c.refresh(ctx, cs); err != nil {
			return nil, err
		}
	}
	s := NewSnapshot(c.ns, pods, c.svcs, c.slices, c.policies)
	s.Warnings = append(s.Warnings, c.warnings...)
	return s, nil
}

func (c *cachedLists) refresh(ctx context.Context, cs kubernetes.Interface) error {
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var warnings []string
	soft := func(kind string, err error) error {
		if err == nil {
			return nil
		}
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			warnings = append(warnings, fmt.Sprintf("Cannot list %s (%s); the analysis treats them as absent.", kind, reasonOf(err)))
			return nil
		}
		return fmt.Errorf("%s: %w", kind, err)
	}
	opts := metav1.ListOptions{}
	var ns []*corev1.Namespace
	if l, err := cs.CoreV1().Namespaces().List(lctx, opts); soft("namespaces", err) != nil {
		return err
	} else if err == nil {
		for i := range l.Items {
			ns = append(ns, &l.Items[i])
		}
	}
	var svcs []*corev1.Service
	if l, err := cs.CoreV1().Services("").List(lctx, opts); soft("Services", err) != nil {
		return err
	} else if err == nil {
		for i := range l.Items {
			svcs = append(svcs, &l.Items[i])
		}
	}
	var slices []*discoveryv1.EndpointSlice
	if l, err := cs.DiscoveryV1().EndpointSlices("").List(lctx, opts); soft("EndpointSlices", err) != nil {
		return err
	} else if err == nil {
		for i := range l.Items {
			slices = append(slices, &l.Items[i])
		}
	}
	var policies []*netv1.NetworkPolicy
	if l, err := cs.NetworkingV1().NetworkPolicies("").List(lctx, opts); soft("NetworkPolicies", err) != nil {
		return err
	} else if err == nil {
		for i := range l.Items {
			policies = append(policies, &l.Items[i])
		}
	}
	c.ns, c.svcs, c.slices, c.policies, c.warnings, c.at = ns, svcs, slices, policies, warnings, time.Now()
	return nil
}

func reasonOf(err error) string {
	if apierrors.IsForbidden(err) {
		return "forbidden by RBAC"
	}
	return "not found"
}
