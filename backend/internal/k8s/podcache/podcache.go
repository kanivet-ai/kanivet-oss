// Package podcache keeps a watch-backed, slimmed-down copy of every pod in a
// cluster, so features that aggregate over all pods (the overview dashboard,
// FinOps) read from memory instead of re-listing.
//
// A full pod LIST is expensive on large clusters: pods carry env vars, probes,
// volume definitions and annotations (vcluster stamps service account tokens
// on every synced pod), so 2,000 pods can be hundreds of MB and tens of
// seconds per list. The cache pays for one list per cluster, follows changes
// with a watch, and keeps only the fields its readers use.
package podcache

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	toolscache "k8s.io/client-go/tools/cache"
)

const (
	// DefaultIdleTimeout stops a cluster's watch after this long without reads.
	DefaultIdleTimeout = 10 * time.Minute
	resyncPeriod       = 0
)

// ClientFunc returns a typed client for a cluster.
type ClientFunc func(cluster string) (kubernetes.Interface, error)

// Lister is what readers of the cache depend on.
type Lister interface {
	List(ctx context.Context, cluster string) ([]*corev1.Pod, error)
}

type entry struct {
	informer toolscache.SharedIndexInformer
	stop     chan struct{}
	lastUsed time.Time
}

type Cache struct {
	client      ClientFunc
	idleTimeout time.Duration

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
	done    chan struct{}
}

func New(client ClientFunc, idleTimeout time.Duration) *Cache {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	c := &Cache{
		client:      client,
		idleTimeout: idleTimeout,
		entries:     map[string]*entry{},
		done:        make(chan struct{}),
	}
	go c.janitor()
	return c
}

// List returns every pod in the cluster (all phases), waiting for the first
// sync if the cluster's watch has just started. The pods are shared, slimmed
// copies (see slim): callers must not modify them.
func (c *Cache) List(ctx context.Context, cluster string) ([]*corev1.Pod, error) {
	e, err := c.entryFor(cluster)
	if err != nil {
		return nil, err
	}
	if !e.informer.HasSynced() {
		if !toolscache.WaitForCacheSync(ctx.Done(), e.informer.HasSynced) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("pod cache for %s not synced: %w", cluster, err)
			}
			return nil, fmt.Errorf("pod cache for %s stopped before syncing", cluster)
		}
	}
	items := e.informer.GetStore().List()
	pods := make([]*corev1.Pod, 0, len(items))
	for _, obj := range items {
		if p, ok := obj.(*corev1.Pod); ok {
			pods = append(pods, p)
		}
	}
	return pods, nil
}

// Warm starts the cluster's watch without waiting for it to sync.
func (c *Cache) Warm(cluster string) {
	_, _ = c.entryFor(cluster)
}

// Forget stops and drops a cluster's watch, e.g. when its credentials or
// connection change.
func (c *Cache) Forget(cluster string) {
	c.mu.Lock()
	e := c.entries[cluster]
	delete(c.entries, cluster)
	c.mu.Unlock()
	if e != nil {
		close(e.stop)
	}
}

func (c *Cache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	entries := c.entries
	c.entries = map[string]*entry{}
	c.mu.Unlock()
	close(c.done)
	for _, e := range entries {
		close(e.stop)
	}
}

func (c *Cache) entryFor(cluster string) (*entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("pod cache closed")
	}
	if e, ok := c.entries[cluster]; ok {
		e.lastUsed = time.Now()
		return e, nil
	}
	if _, err := c.client(cluster); err != nil {
		return nil, err
	}
	// The client is resolved on every list and watch: a vcluster that
	// reconnects comes back on a new port-forward and a new client.
	informer := toolscache.NewSharedIndexInformer(
		&toolscache.ListWatch{
			ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
				cs, err := c.client(cluster)
				if err != nil {
					return nil, err
				}
				return cs.CoreV1().Pods("").List(ctx, opts)
			},
			WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				cs, err := c.client(cluster)
				if err != nil {
					return nil, err
				}
				return cs.CoreV1().Pods("").Watch(ctx, opts)
			},
		},
		&corev1.Pod{},
		resyncPeriod,
		toolscache.Indexers{},
	)
	_ = informer.SetTransform(slim)
	e := &entry{informer: informer, stop: make(chan struct{}), lastUsed: time.Now()}
	c.entries[cluster] = e
	go informer.Run(e.stop)
	return e, nil
}

func (c *Cache) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.evictIdle(time.Now())
		}
	}
}

func (c *Cache) evictIdle(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for cluster, e := range c.entries {
		if now.Sub(e.lastUsed) > c.idleTimeout {
			delete(c.entries, cluster)
			close(e.stop)
		}
	}
}

// keptAnnotationPrefix keeps the vcluster syncer's identity and owner
// annotations (FinOps maps synced pods back to their virtual workloads).
// Token annotations under the same prefix are large and unused.
const keptAnnotationPrefix = "vcluster.loft.sh/"

// slim drops what readers never use: managed fields, most annotations, env,
// probes, volumes, images and so on. It keeps identity, labels, owners,
// scheduling, container resources and status.
func slim(obj interface{}) (interface{}, error) {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return obj, nil
	}
	out := &corev1.Pod{
		TypeMeta: p.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:              p.Name,
			Namespace:         p.Namespace,
			UID:               p.UID,
			ResourceVersion:   p.ResourceVersion,
			CreationTimestamp: p.CreationTimestamp,
			DeletionTimestamp: p.DeletionTimestamp,
			Labels:            p.Labels,
			OwnerReferences:   p.OwnerReferences,
		},
		Spec: corev1.PodSpec{
			NodeName:          p.Spec.NodeName,
			Containers:        slimContainers(p.Spec.Containers),
			InitContainers:    slimContainers(p.Spec.InitContainers),
			Overhead:          p.Spec.Overhead,
			PriorityClassName: p.Spec.PriorityClassName,
		},
		Status: corev1.PodStatus{
			Phase:                 p.Status.Phase,
			Conditions:            p.Status.Conditions,
			Reason:                p.Status.Reason,
			StartTime:             p.Status.StartTime,
			ContainerStatuses:     slimStatuses(p.Status.ContainerStatuses),
			InitContainerStatuses: slimStatuses(p.Status.InitContainerStatuses),
		},
	}
	for k, v := range p.Annotations {
		if strings.HasPrefix(k, keptAnnotationPrefix) && !strings.HasPrefix(k, keptAnnotationPrefix+"token-") {
			if out.Annotations == nil {
				out.Annotations = map[string]string{}
			}
			out.Annotations[k] = v
		}
	}
	return out, nil
}

func slimContainers(in []corev1.Container) []corev1.Container {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.Container, len(in))
	for i, c := range in {
		out[i] = corev1.Container{Name: c.Name, Resources: c.Resources, RestartPolicy: c.RestartPolicy}
	}
	return out
}

func slimStatuses(in []corev1.ContainerStatus) []corev1.ContainerStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.ContainerStatus, len(in))
	for i, s := range in {
		out[i] = corev1.ContainerStatus{
			Name:                 s.Name,
			Ready:                s.Ready,
			RestartCount:         s.RestartCount,
			State:                s.State,
			LastTerminationState: s.LastTerminationState,
			Started:              s.Started,
		}
	}
	return out
}
