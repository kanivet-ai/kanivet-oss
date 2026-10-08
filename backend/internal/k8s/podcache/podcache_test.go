package podcache

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kanivet/backend/internal/k8s/watcher"
)

func heavyPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "beige",
			Labels:    map[string]string{"vcluster.loft.sh/managed-by": "beige-vcluster"},
			Annotations: map[string]string{
				"vcluster.loft.sh/object-name":                     "api-0",
				"vcluster.loft.sh/token-abcdefgh":                  "eyJhbGciOi...",
				"kubectl.kubernetes.io/last-applied-configuration": "{...}",
			},
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubelet"}},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-a",
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "registry/app:1",
				Env:   []corev1.EnvVar{{Name: "SECRET", Value: "x"}},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
				},
			}},
			Volumes: []corev1.Volume{{Name: "data"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// fakeSource stands in for the watcher service: it counts the references the
// cache takes on each cluster's pods watch and lets a test play the watch.
type fakeSource struct {
	mu        sync.Mutex
	sink      watcher.ObjectSink
	retained  map[string]int
	retains   int
	retainErr error
	// onRetain runs when a cluster's watch is first asked for, the way the
	// watcher starts one.
	onRetain func(cluster string)
}

func (s *fakeSource) SetObjectSink(group, version, kind string, sink watcher.ObjectSink) {
	if group != "" || version != "v1" || kind != "pods" {
		panic("the cache registered for " + group + "/" + version + "/" + kind)
	}
	s.sink = sink
}

func (s *fakeSource) Retain(cluster, group, version, kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retainErr != nil {
		return s.retainErr
	}
	if s.retained == nil {
		s.retained = map[string]int{}
	}
	s.retained[cluster]++
	s.retains++
	if s.retained[cluster] == 1 && s.onRetain != nil {
		go s.onRetain(cluster)
	}
	return nil
}

func (s *fakeSource) Release(cluster, group, version, kind string) {
	s.mu.Lock()
	s.retained[cluster]--
	s.mu.Unlock()
}

func (s *fakeSource) refs(cluster string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retained[cluster]
}

// newCache returns a cache whose clusters sync to pods as soon as they are
// read.
func newCache(t *testing.T, pods ...*corev1.Pod) (*Cache, *fakeSource) {
	t.Helper()
	src := &fakeSource{}
	c := New(src, time.Minute)
	src.onRetain = func(cluster string) { list(c.Attach(cluster), pods...) }
	t.Cleanup(c.Close)
	return c, src
}

// list plays one full listing into a feed.
func list(f watcher.ObjectFeed, pods ...*corev1.Pod) watcher.ObjectFeed {
	f.Reset()
	for _, p := range pods {
		f.Put(p)
	}
	f.Synced()
	return f
}

func names(pods []*corev1.Pod) []string {
	out := make([]string, len(pods))
	for i, p := range pods {
		out[i] = p.Name
	}
	sort.Strings(out)
	return out
}

func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func mustList(t *testing.T, c *Cache, cluster string) []*corev1.Pod {
	t.Helper()
	pods, err := c.List(shortCtx(t), cluster)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return pods
}

func TestListSyncsAndSlimsPods(t *testing.T) {
	c, _ := newCache(t, heavyPod("api-0-x-apps-x-beige-vcluster"))

	pods := mustList(t, c, "prod")
	if len(pods) != 1 {
		t.Fatalf("got %d pods", len(pods))
	}
	p := pods[0]
	if p.Spec.NodeName != "node-a" || p.Spec.Containers[0].Resources.Requests.Cpu().MilliValue() != 250 {
		t.Errorf("scheduling or resources lost: %+v", p.Spec)
	}
	if p.Labels["vcluster.loft.sh/managed-by"] != "beige-vcluster" || p.Annotations["vcluster.loft.sh/object-name"] != "api-0" {
		t.Errorf("vcluster identity lost: %v %v", p.Labels, p.Annotations)
	}
	if len(p.Annotations) != 1 || p.ManagedFields != nil || p.Spec.Containers[0].Env != nil || p.Spec.Volumes != nil {
		t.Errorf("heavy fields kept: annotations=%v env=%v volumes=%v", p.Annotations, p.Spec.Containers[0].Env, p.Spec.Volumes)
	}
}

// A read never returns part of a listing: it waits for the listing to end.
func TestListWaitsForTheFirstSync(t *testing.T) {
	src := &fakeSource{}
	c := New(src, time.Minute)
	defer c.Close()
	f := c.Attach("prod")
	f.Reset()
	f.Put(heavyPod("a"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.List(ctx, "prod"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("List of an unfinished listing: err = %v, want the deadline", err)
	}

	got := make(chan []*corev1.Pod, 1)
	go func() { got <- mustList(t, c, "prod") }()
	select {
	case pods := <-got:
		t.Fatalf("List returned %d pods before the listing ended", len(pods))
	case <-time.After(20 * time.Millisecond):
	}
	f.Put(heavyPod("b"))
	f.Synced()
	if n := names(<-got); !slices.Equal(n, []string{"a", "b"}) {
		t.Fatalf("pods = %v", n)
	}
}

func TestListFollowsChanges(t *testing.T) {
	src := &fakeSource{}
	c := New(src, time.Minute)
	defer c.Close()
	f := list(c.Attach("prod"), heavyPod("a"), heavyPod("b"))

	f.Put(heavyPod("c"))
	f.Delete(heavyPod("a"))
	restarted := heavyPod("b")
	restarted.Status.Phase = corev1.PodFailed
	f.Put(restarted)

	pods := mustList(t, c, "prod")
	if n := names(pods); !slices.Equal(n, []string{"b", "c"}) {
		t.Fatalf("pods = %v", n)
	}
	for _, p := range pods {
		if p.Name == "b" && p.Status.Phase != corev1.PodFailed {
			t.Fatalf("b is still %s", p.Status.Phase)
		}
	}
}

// A relist replaces the pods when it completes: until then the old ones are
// served whole, and after it the ones it did not include are gone.
func TestRelistDropsPodsThatAreGone(t *testing.T) {
	src := &fakeSource{}
	c := New(src, time.Minute)
	defer c.Close()
	f := list(c.Attach("prod"), heavyPod("a"), heavyPod("b"))
	f.Put(heavyPod("c"))

	f.Reset()
	f.Put(heavyPod("b"))
	if n := names(mustList(t, c, "prod")); !slices.Equal(n, []string{"a", "b", "c"}) {
		t.Fatalf("pods during the relist = %v, want the previous listing whole", n)
	}
	f.Put(heavyPod("d"))
	f.Synced()
	if n := names(mustList(t, c, "prod")); !slices.Equal(n, []string{"b", "d"}) {
		t.Fatalf("pods after the relist = %v", n)
	}
}

// A watch on the dynamic client hands pods over unstructured.
func TestUnstructuredPodsAreAccepted(t *testing.T) {
	src := &fakeSource{}
	c := New(src, time.Minute)
	defer c.Close()
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(heavyPod("a"))
	if err != nil {
		t.Fatal(err)
	}
	f := c.Attach("prod")
	f.Reset()
	f.Put(&unstructured.Unstructured{Object: m})
	f.Put(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "not-a-pod"}})
	f.Synced()

	pods := mustList(t, c, "prod")
	if len(pods) != 1 || pods[0].Spec.NodeName != "node-a" || pods[0].ManagedFields != nil {
		t.Fatalf("pods = %+v", pods)
	}
	f.Delete(&unstructured.Unstructured{Object: m})
	if pods := mustList(t, c, "prod"); len(pods) != 0 {
		t.Fatalf("%d pods left after the delete", len(pods))
	}
}

// However many readers there are, the cache holds each cluster's watch once.
func TestReadersShareOneReferenceOnTheWatch(t *testing.T) {
	c, src := newCache(t, heavyPod("a"))
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mustList(t, c, "prod")
		}()
	}
	wg.Wait()
	mustList(t, c, "staging")
	if src.refs("prod") != 1 || src.refs("staging") != 1 || src.retains != 2 {
		t.Fatalf("references: prod=%d staging=%d, %d Retain calls; want 1, 1 and 2", src.refs("prod"), src.refs("staging"), src.retains)
	}
}

func TestIdleClustersAreReleased(t *testing.T) {
	c, src := newCache(t, heavyPod("a"))
	mustList(t, c, "prod")
	mustList(t, c, "staging")
	c.mu.Lock()
	c.clusters["prod"].lastUsed = time.Now().Add(-2 * time.Minute)
	c.mu.Unlock()

	c.releaseIdle(time.Now())
	if src.refs("prod") != 0 || src.refs("staging") != 1 {
		t.Fatalf("references: prod=%d (want 0), staging=%d (want 1)", src.refs("prod"), src.refs("staging"))
	}

	// The watch outlives the reference for a while (a Pods list, the
	// watcher's grace period): the pods are still current and a new read
	// takes the reference back without waiting for anything.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if pods, err := c.List(ctx, "prod"); err != nil || len(pods) != 1 {
		t.Fatalf("read after release: %d pods, err %v", len(pods), err)
	}
	if src.refs("prod") != 1 {
		t.Fatalf("prod references = %d after a new read, want 1", src.refs("prod"))
	}
}

// When the watch ends its pods are no longer kept current, so they are not
// served: the next read waits for the next watch.
func TestPodsOfAnEndedWatchAreNotServed(t *testing.T) {
	src := &fakeSource{}
	c := New(src, time.Minute)
	defer c.Close()
	old := list(c.Attach("prod"), heavyPod("a"))
	mustList(t, c, "prod")
	old.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.List(ctx, "prod"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read with no watch: err = %v, want it to wait", err)
	}

	next := list(c.Attach("prod"), heavyPod("b"))
	// The ended watch still unwinding must not take its replacement down.
	old.Close()
	old.Put(heavyPod("stale"))
	if n := names(mustList(t, c, "prod")); !slices.Equal(n, []string{"b"}) {
		t.Fatalf("pods = %v", n)
	}

	// A cluster nothing feeds and nobody holds is forgotten.
	c.mu.Lock()
	c.clusters["prod"].lastUsed = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	c.releaseIdle(time.Now())
	next.Close()
	c.mu.Lock()
	_, kept := c.clusters["prod"]
	c.mu.Unlock()
	if kept {
		t.Fatal("the cluster's state outlived its watch and its readers")
	}
}

func TestRetainFailureReachesTheReader(t *testing.T) {
	src := &fakeSource{retainErr: errors.New("no such cluster")}
	c := New(src, time.Minute)
	defer c.Close()
	if _, err := c.List(shortCtx(t), "prod"); err == nil || err.Error() != "no such cluster" {
		t.Fatalf("err = %v", err)
	}
	c.mu.Lock()
	n := len(c.clusters)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d clusters kept after a failed read", n)
	}
}

func TestCloseReleasesAndUnblocksReaders(t *testing.T) {
	src := &fakeSource{}
	c := New(src, time.Minute)
	errc := make(chan error, 1)
	go func() {
		_, err := c.List(context.Background(), "prod")
		errc <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for src.refs("prod") != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the reader never asked for the watch")
		}
		time.Sleep(time.Millisecond)
	}
	c.Close()
	if err := <-errc; !errors.Is(err, errClosed) {
		t.Fatalf("err = %v, want the cache closed", err)
	}
	if src.refs("prod") != 0 {
		t.Fatalf("prod references = %d after Close", src.refs("prod"))
	}
	if _, err := c.List(context.Background(), "prod"); !errors.Is(err, errClosed) {
		t.Fatalf("read after Close: err = %v", err)
	}
	c.Close()
}

func TestSlimKeepsOnlyJVMOptions(t *testing.T) {
	p := heavyPod("jvm")
	p.Spec.Containers[0].Env = append(p.Spec.Containers[0].Env, corev1.EnvVar{Name: "JAVA_TOOL_OPTIONS", Value: "-XX:MaxRAMPercentage=75"})
	p.Spec.Containers[0].Command = []string{"/opt/java/bin/java"}
	p.Spec.Containers[0].Args = []string{"-Xmx512m", "-jar", "app.jar", "--password=hunter2"}
	c := slim(p).Spec.Containers[0]
	if len(c.Env) != 1 || c.Env[0].Name != "JAVA_TOOL_OPTIONS" {
		t.Fatalf("env %v", c.Env)
	}
	if !slices.Equal(c.Args, []string{"/opt/java/bin/java", "-Xmx512m"}) || c.Image != "registry/app:1" {
		t.Fatalf("args %v image %q", c.Args, c.Image)
	}
}
