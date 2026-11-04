package health

import (
	"context"
	"sort"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kanivet/backend/internal/db"
)

// Snapshot is everything the rules and checks read. It is plain data so the
// rules are tested without a cluster.
type Snapshot struct {
	Now             time.Time
	Pods            []*corev1.Pod
	Deployments     []appsv1.Deployment
	StatefulSets    []appsv1.StatefulSet
	DaemonSets      []appsv1.DaemonSet
	Jobs            []batchv1.Job
	CronJobs        []batchv1.CronJob
	Services        []corev1.Service
	Ingresses       []networkingv1.Ingress
	PVCs            []corev1.PersistentVolumeClaim
	Nodes           []corev1.Node
	HPAs            []autoscalingv2.HorizontalPodAutoscaler
	PDBs            []policyv1.PodDisruptionBudget
	NetworkPolicies []networkingv1.NetworkPolicy
	Events          []db.K8sEvent
	// Unavailable are kinds that could not be read.
	Unavailable []string
}

// ClientProvider is the k8s client, as the loader uses it.
type ClientProvider interface {
	GetClientForCluster(cluster string) (kubernetes.Interface, error)
}

// PodLister is the shared pod cache.
type PodLister interface {
	List(ctx context.Context, cluster string) ([]*corev1.Pod, error)
}

// EventSource returns the newest stored events of a cluster.
type EventSource func(cluster string, n int) ([]db.K8sEvent, bool)

// Loader builds snapshots. Pods come from the shared pod cache, which the Pods
// list's watch feeds. The watcher slims workload templates away, so the other
// kinds are listed through the typed client and kept for ttl: one listing
// serves the Issues page, the Audit page, the inspector and the sidebar badge.
type Loader struct {
	clients ClientProvider
	pods    PodLister
	events  EventSource
	ttl     time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]*cachedKinds
}

type cachedKinds struct {
	at   time.Time
	snap *Snapshot // kinds other than pods and events
	wait chan struct{}
}

const defaultTTL = 20 * time.Second

func NewLoader(clients ClientProvider, pods PodLister, events EventSource) *Loader {
	return &Loader{clients: clients, pods: pods, events: events, ttl: defaultTTL, now: time.Now, cache: map[string]*cachedKinds{}}
}

// Load returns a snapshot of the cluster. Kinds that fail to list (forbidden,
// not served) are named in Unavailable and left empty.
func (l *Loader) Load(ctx context.Context, cluster string) (*Snapshot, error) {
	base, err := l.kinds(ctx, cluster)
	if err != nil {
		return nil, err
	}
	snap := *base
	snap.Now = l.now()
	snap.Unavailable = append([]string(nil), base.Unavailable...)
	if l.pods != nil {
		pods, err := l.pods.List(ctx, cluster)
		if err != nil {
			snap.Unavailable = append(snap.Unavailable, "Pod")
		} else {
			snap.Pods = pods
		}
	}
	if l.events != nil {
		if evs, ok := l.events(cluster, 2000); ok {
			snap.Events = evs
		}
	}
	sort.Strings(snap.Unavailable)
	return &snap, nil
}

// Forget drops the cached listing so the next Load lists again.
func (l *Loader) Forget(cluster string) {
	l.mu.Lock()
	delete(l.cache, cluster)
	l.mu.Unlock()
}

func (l *Loader) kinds(ctx context.Context, cluster string) (*Snapshot, error) {
	for {
		l.mu.Lock()
		c := l.cache[cluster]
		if c != nil && c.wait != nil {
			w := c.wait
			l.mu.Unlock()
			select {
			case <-w:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if c != nil && c.snap != nil && l.now().Sub(c.at) < l.ttl {
			l.mu.Unlock()
			return c.snap, nil
		}
		w := make(chan struct{})
		l.cache[cluster] = &cachedKinds{wait: w}
		l.mu.Unlock()

		snap, err := l.list(ctx, cluster)
		l.mu.Lock()
		if err != nil {
			delete(l.cache, cluster)
		} else {
			l.cache[cluster] = &cachedKinds{at: l.now(), snap: snap}
		}
		l.mu.Unlock()
		close(w)
		return snap, err
	}
}

func (l *Loader) list(ctx context.Context, cluster string) (*Snapshot, error) {
	cs, err := l.clients.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return ListKinds(ctx, cs), nil
}

// ListKinds lists every non-pod kind the rules read, in parallel.
func ListKinds(ctx context.Context, cs kubernetes.Interface) *Snapshot {
	s := &Snapshot{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	opts := metav1.ListOptions{ResourceVersion: "0"}
	run := func(kind string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				mu.Lock()
				s.Unavailable = append(s.Unavailable, kind)
				mu.Unlock()
			}
		}()
	}
	run("Deployment", func() error {
		r, err := cs.AppsV1().Deployments("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.Deployments = r.Items
			mu.Unlock()
		}
		return err
	})
	run("StatefulSet", func() error {
		r, err := cs.AppsV1().StatefulSets("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.StatefulSets = r.Items
			mu.Unlock()
		}
		return err
	})
	run("DaemonSet", func() error {
		r, err := cs.AppsV1().DaemonSets("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.DaemonSets = r.Items
			mu.Unlock()
		}
		return err
	})
	run("Job", func() error {
		r, err := cs.BatchV1().Jobs("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.Jobs = r.Items
			mu.Unlock()
		}
		return err
	})
	run("CronJob", func() error {
		r, err := cs.BatchV1().CronJobs("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.CronJobs = r.Items
			mu.Unlock()
		}
		return err
	})
	run("Service", func() error {
		r, err := cs.CoreV1().Services("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.Services = r.Items
			mu.Unlock()
		}
		return err
	})
	run("Ingress", func() error {
		r, err := cs.NetworkingV1().Ingresses("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.Ingresses = r.Items
			mu.Unlock()
		}
		return err
	})
	run("PersistentVolumeClaim", func() error {
		r, err := cs.CoreV1().PersistentVolumeClaims("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.PVCs = r.Items
			mu.Unlock()
		}
		return err
	})
	run("Node", func() error {
		r, err := cs.CoreV1().Nodes().List(ctx, opts)
		if err == nil {
			for i := range r.Items {
				r.Items[i].Status.Images = nil
				r.Items[i].ManagedFields = nil
			}
			mu.Lock()
			s.Nodes = r.Items
			mu.Unlock()
		}
		return err
	})
	run("HorizontalPodAutoscaler", func() error {
		r, err := cs.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.HPAs = r.Items
			mu.Unlock()
		}
		return err
	})
	run("PodDisruptionBudget", func() error {
		r, err := cs.PolicyV1().PodDisruptionBudgets("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.PDBs = r.Items
			mu.Unlock()
		}
		return err
	})
	run("NetworkPolicy", func() error {
		r, err := cs.NetworkingV1().NetworkPolicies("").List(ctx, opts)
		if err == nil {
			mu.Lock()
			s.NetworkPolicies = r.Items
			mu.Unlock()
		}
		return err
	})
	wg.Wait()
	return s
}
