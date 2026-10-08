// Package podcache keeps a slimmed-down copy of every pod in a cluster, so
// features that aggregate over all pods (the overview dashboard, FinOps,
// rightsizing) read from memory instead of re-listing.
//
// A full pod LIST is expensive on large clusters: pods carry env vars, probes,
// volume definitions and annotations (vcluster stamps service account tokens
// on every synced pod), so 2,000 pods can be hundreds of MB and tens of
// seconds per list. The cache therefore does not list or watch pods itself.
// The watcher service already does both for the Pods list, and the cache is
// that watch's object sink: one list and one watch per cluster feed the list
// rows and every reader here. The cache only asks for the watch to be kept
// running while it is read, and keeps the fields its readers use.
package podcache

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kanivet/backend/internal/k8s/watcher"
)

// DefaultIdleTimeout is how long a cluster's watch is kept running for the
// cache after its last read.
const DefaultIdleTimeout = 10 * time.Minute

// The pods watch, as the watcher service and its subscribers name it.
const (
	podGroup   = ""
	podVersion = "v1"
	podKind    = "pods"
)

// Source is the watcher service, as the cache uses it.
type Source interface {
	SetObjectSink(group, version, kind string, sink watcher.ObjectSink)
	Retain(cluster, group, version, kind string) error
	Release(cluster, group, version, kind string)
}

// Lister is what readers of the cache depend on.
type Lister interface {
	List(ctx context.Context, cluster string) ([]*corev1.Pod, error)
}

var errClosed = errors.New("pod cache closed")

type Cache struct {
	source      Source
	idleTimeout time.Duration

	mu       sync.Mutex
	clusters map[string]*cluster
	closed   bool
	done     chan struct{}
}

// cluster is the cache's state for one cluster.
type cluster struct {
	// feed is the running pods watch's, nil while there is none.
	feed *feed
	// retained says the cache holds the watch for its readers.
	retained bool
	lastUsed time.Time
	// changed is closed, and replaced, when feed or its synced changes or a
	// reader has to try again.
	changed chan struct{}
}

// New returns a cache fed by source's pods watches.
func New(source Source, idleTimeout time.Duration) *Cache {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	c := &Cache{
		source:      source,
		idleTimeout: idleTimeout,
		clusters:    map[string]*cluster{},
		done:        make(chan struct{}),
	}
	source.SetObjectSink(podGroup, podVersion, podKind, c)
	go c.janitor()
	return c
}

// List returns every pod in the cluster (all phases), waiting for the first
// sync if the cluster's watch has just started. The pods are shared, slimmed
// copies (see slim): callers must not modify them.
func (c *Cache) List(ctx context.Context, name string) ([]*corev1.Pod, error) {
	for {
		if err := c.retain(name); err != nil {
			return nil, err
		}
		c.mu.Lock()
		cl := c.clusters[name]
		if cl == nil {
			c.mu.Unlock()
			return nil, errClosed
		}
		if f := cl.feed; f != nil && f.synced {
			pods := make([]*corev1.Pod, 0, len(f.pods))
			for _, p := range f.pods {
				pods = append(pods, p.pod)
			}
			c.mu.Unlock()
			return pods, nil
		}
		changed := cl.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-c.done:
			return nil, errClosed
		case <-ctx.Done():
			return nil, fmt.Errorf("pod cache for %s not synced: %w", name, ctx.Err())
		}
	}
}

// retain notes a read of the cluster and makes sure its pods watch is kept
// running for the cache.
func (c *Cache) retain(name string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errClosed
	}
	cl := c.cluster(name)
	cl.lastUsed = time.Now()
	if cl.retained {
		c.mu.Unlock()
		return nil
	}
	cl.retained = true
	c.mu.Unlock()
	// Not under the lock: a watch this starts attaches to the cache.
	err := c.source.Retain(name, podGroup, podVersion, podKind)
	if err != nil {
		c.mu.Lock()
		cl.retained = false
		c.notify(cl)
		c.dropUnused(name, cl)
		c.mu.Unlock()
	}
	return err
}

// cluster returns the cluster's state, creating it. The caller holds c.mu.
func (c *Cache) cluster(name string) *cluster {
	cl := c.clusters[name]
	if cl == nil {
		cl = &cluster{changed: make(chan struct{})}
		c.clusters[name] = cl
	}
	return cl
}

// notify wakes the readers waiting on the cluster. The caller holds c.mu.
func (c *Cache) notify(cl *cluster) {
	close(cl.changed)
	cl.changed = make(chan struct{})
}

// dropUnused forgets a cluster nothing feeds and nobody reads. The caller
// holds c.mu.
func (c *Cache) dropUnused(name string, cl *cluster) {
	if cl.feed == nil && !cl.retained && c.clusters[name] == cl {
		delete(c.clusters, name)
	}
}

func (c *Cache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	var retained []string
	for name, cl := range c.clusters {
		if cl.retained {
			retained = append(retained, name)
		}
	}
	c.clusters = map[string]*cluster{}
	c.mu.Unlock()
	close(c.done)
	for _, name := range retained {
		c.source.Release(name, podGroup, podVersion, podKind)
	}
}

func (c *Cache) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.releaseIdle(time.Now())
		}
	}
}

// releaseIdle gives back the watches of the clusters nobody has read for the
// idle timeout. Their pods stay current for as long as something else, a
// Pods list or the watcher's own grace period, keeps the watch running.
func (c *Cache) releaseIdle(now time.Time) {
	c.mu.Lock()
	var idle []string
	for name, cl := range c.clusters {
		if cl.retained && now.Sub(cl.lastUsed) > c.idleTimeout {
			cl.retained = false
			c.dropUnused(name, cl)
			idle = append(idle, name)
		}
	}
	c.mu.Unlock()
	for _, name := range idle {
		c.source.Release(name, podGroup, podVersion, podKind)
	}
}

// feed is one pods watch's contribution: the cluster's pods as that watch
// knows them. Its fields are guarded by the cache's mutex.
type feed struct {
	cache   *Cache
	cluster string
	pods    map[string]listed
	// listing numbers the full listings; a pod carries the number of the one
	// that was current when it was last seen.
	listing uint64
	synced  bool
}

type listed struct {
	pod     *corev1.Pod
	listing uint64
}

// Attach makes the watch that is starting the cluster's source of pods.
func (c *Cache) Attach(name string) watcher.ObjectFeed {
	f := &feed{cache: c, cluster: name, pods: map[string]listed{}}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		cl := c.cluster(name)
		cl.feed = f
		c.notify(cl)
	}
	return f
}

// Reset opens a listing. The pods already held keep being served until it
// completes, so a relist is never seen as an empty cluster.
func (f *feed) Reset() {
	f.cache.mu.Lock()
	f.listing++
	f.cache.mu.Unlock()
}

func (f *feed) Put(obj runtime.Object) {
	p := podOf(obj)
	if p == nil {
		return
	}
	p = slim(p)
	f.cache.mu.Lock()
	f.pods[p.Namespace+"/"+p.Name] = listed{pod: p, listing: f.listing}
	f.cache.mu.Unlock()
}

func (f *feed) Delete(obj runtime.Object) {
	m, err := meta.Accessor(obj)
	if err != nil {
		return
	}
	f.cache.mu.Lock()
	delete(f.pods, m.GetNamespace()+"/"+m.GetName())
	f.cache.mu.Unlock()
}

// Synced drops the pods the listing did not include and, the first time,
// lets the readers waiting for it in.
func (f *feed) Synced() {
	c := f.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, p := range f.pods {
		if p.listing != f.listing {
			delete(f.pods, key)
		}
	}
	if f.synced {
		return
	}
	f.synced = true
	if cl := c.clusters[f.cluster]; cl != nil && cl.feed == f {
		c.notify(cl)
	}
}

// Close detaches the feed. Nothing keeps its pods current any more, so they
// are not served again: the next read waits for the next watch's listing.
func (f *feed) Close() {
	c := f.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl := c.clusters[f.cluster]; cl != nil && cl.feed == f {
		cl.feed = nil
		c.notify(cl)
		c.dropUnused(f.cluster, cl)
	}
}

// podOf returns the pod a watch handed over: typed as a rule, unstructured
// when the watch runs on the dynamic client.
func podOf(obj runtime.Object) *corev1.Pod {
	switch o := obj.(type) {
	case *corev1.Pod:
		return o
	case *unstructured.Unstructured:
		p := &corev1.Pod{}
		if runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, p) == nil {
			return p
		}
	}
	return nil
}

// keptAnnotationPrefix keeps the vcluster syncer's identity and owner
// annotations (FinOps maps synced pods back to their virtual workloads).
// Token annotations under the same prefix are large and unused.
const keptAnnotationPrefix = "vcluster.loft.sh/"

// slim drops what readers never use: managed fields, most annotations, env,
// probes, volumes, images and so on. It keeps identity, labels, owners,
// scheduling, container resources and status.
func slim(p *corev1.Pod) *corev1.Pod {
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
	return out
}

func slimContainers(in []corev1.Container) []corev1.Container {
	if len(in) == 0 {
		return nil
	}
	out := make([]corev1.Container, len(in))
	for i, c := range in {
		out[i] = corev1.Container{Name: c.Name, Image: c.Image, Resources: c.Resources, RestartPolicy: c.RestartPolicy}
		// Rightsizing tells JVMs apart by how their heap is sized, so the
		// JVM options survive slimming; nothing else in env or args does.
		for _, e := range c.Env {
			if jvmOptionVars[e.Name] && e.Value != "" {
				out[i].Env = append(out[i].Env, corev1.EnvVar{Name: e.Name, Value: e.Value})
			}
		}
		for _, a := range append(slices.Clone(c.Command), c.Args...) {
			if isJVMFlag(a) {
				out[i].Args = append(out[i].Args, a)
			}
		}
	}
	return out
}

// jvmOptionVars are the environment variables the JVM, or the common image
// entrypoints, read options from.
var jvmOptionVars = map[string]bool{"JAVA_TOOL_OPTIONS": true, "JDK_JAVA_OPTIONS": true, "_JAVA_OPTIONS": true, "JAVA_OPTS": true}

// isJVMFlag keeps the arguments that size a JVM's heap, or mark the process
// as a JVM.
func isJVMFlag(a string) bool {
	return strings.Contains(a, "-Xmx") || strings.Contains(a, "RAMPercentage") || strings.Contains(a, "-XX:MaxRAM") ||
		a == "java" || strings.HasSuffix(a, "/java")
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
