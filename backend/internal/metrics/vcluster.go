package metrics

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	"golang.org/x/sync/singleflight"
)

// A vcluster's pods are copies: cAdvisor on the host measures the real pod,
// under the host namespace and the syncer's translated name, and those series
// are in the host cluster's store. A store found inside the vcluster is the
// tenant's own and holds no container metrics. So for a vcluster, Prometheus
// and Mimir mean the host's: they are detected there, with the host's saved
// tenant and service, and charts are asked for under the host pod's names.
// metrics-server is the exception: it answers inside the vcluster, under the
// virtual names.

// vclusterPodResolver is implemented by *k8s.Client.
type vclusterPodResolver interface {
	ResolveVClusterHostPod(vclusterID, namespace, name string) (*k8s.VClusterHostPod, error)
	ResolveVClusterHostPods(vclusterID, namespace string, names []string) (map[string]*k8s.VClusterHostPod, error)
}

// A stream asks on every tick, so lookups are remembered. A host pod's name
// does not change for the life of the virtual pod, but a recreated pod can
// take the same virtual name, hence hostPodTTL. That a pod has no host copy
// is remembered too, more briefly: it is usually about to get one.
const (
	hostPodTTL     = 30 * time.Second
	noHostPodTTL   = 10 * time.Second
	hostPodEntries = 4096
)

// hostPodEntry's pod is nil for a virtual pod with no host copy.
type hostPodEntry struct {
	pod     *k8s.VClusterHostPod
	expires time.Time
}

type hostPodCache struct {
	mu      sync.Mutex
	entries map[string]hostPodEntry
	// lookups shares one lookup between the charts of a pod, which open
	// together.
	lookups singleflight.Group
}

func hostPodKey(vcluster, namespace, name string) string {
	return vcluster + "\x00" + namespace + "\x00" + name
}

func (c *hostPodCache) get(key string) (*k8s.VClusterHostPod, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return e.pod, true
}

func (c *hostPodCache) put(key string, pod *k8s.VClusterHostPod) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]hostPodEntry{}
	}
	if len(c.entries) >= hostPodEntries {
		clear(c.entries)
	}
	ttl := hostPodTTL
	if pod == nil {
		ttl = noHostPodTTL
	}
	c.entries[key] = hostPodEntry{pod: pod, expires: time.Now().Add(ttl)}
}

// hostSeriesProvider marks providers that answer from the host cluster's
// container metrics, which carry the host pod names.
type hostSeriesProvider interface{ readsHostSeries() }

func (*PrometheusProvider) readsHostSeries() {}
func (*MimirProvider) readsHostSeries()      {}

// StoreCluster is the cluster whose Prometheus-compatible store holds a
// cluster's container metrics: a vcluster's host, otherwise the cluster itself.
func StoreCluster(cluster string) string {
	if host, _, _, ok := k8s.ParseVClusterID(cluster); ok {
		return host
	}
	return cluster
}

// providerCluster is the cluster a provider is detected in and queried through.
func providerCluster(cluster string, p Provider) string {
	if _, ok := p.(hostSeriesProvider); ok {
		return StoreCluster(cluster)
	}
	return cluster
}

// hostQuery rewrites a vcluster pod chart's query to the host pod. ok is false
// when the pod has no host copy (not synced yet, or gone): the chart is then
// empty, since the virtual names mean nothing in the host's store and could
// even name some other pod there. Node charts keep the node's name, which the
// vcluster copies from the real node.
func (s *Service) hostQuery(cluster string, q MetricQuery) (MetricQuery, bool, error) {
	if q.NodeName != "" || q.PodName == "" {
		return q, true, nil
	}
	r, _ := s.k8s.(vclusterPodResolver)
	if r == nil {
		return q, false, nil
	}
	key := hostPodKey(cluster, q.Namespace, q.PodName)
	pod, ok := s.hostPods.get(key)
	if !ok {
		found, err, _ := s.hostPods.lookups.Do(key, func() (any, error) {
			pod, err := r.ResolveVClusterHostPod(cluster, q.Namespace, q.PodName)
			if errors.Is(err, k8s.ErrVClusterHostPodNotFound) {
				pod, err = nil, nil
			}
			if err == nil {
				s.hostPods.put(key, pod)
			}
			return pod, err
		})
		if err != nil {
			return q, false, fmt.Errorf("find the host pod of %s/%s: %w", q.Namespace, q.PodName, err)
		}
		pod = found.(*k8s.VClusterHostPod)
	}
	if pod == nil {
		return q, false, nil
	}
	q.Namespace, q.PodName = pod.Namespace, pod.Name
	return q, true, nil
}

// hostWorkloadQuery rewrites a vcluster workload chart's query to host pods
// and returns the virtual name of each host pod, for translating the answer
// back. Pods with no host copy are left out; with none at all, ok is false.
func (s *Service) hostWorkloadQuery(cluster string, q WorkloadMetricQuery) (WorkloadMetricQuery, map[string]string, bool, error) {
	r, _ := s.k8s.(vclusterPodResolver)
	if r == nil {
		return q, nil, false, nil
	}
	hostToVirtual := map[string]string{}
	hostNamespace := ""
	add := func(virtual string, pod *k8s.VClusterHostPod) {
		hostNamespace = pod.Namespace
		hostToVirtual[pod.Name] = virtual
	}
	var missing []string
	for _, name := range q.PodNames {
		if pod, ok := s.hostPods.get(hostPodKey(cluster, q.Namespace, name)); ok {
			if pod != nil {
				add(name, pod)
			}
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		found, err := r.ResolveVClusterHostPods(cluster, q.Namespace, missing)
		if err != nil {
			return q, nil, false, fmt.Errorf("find the host pods of %s: %w", q.Namespace, err)
		}
		for _, name := range missing {
			pod := found[name]
			s.hostPods.put(hostPodKey(cluster, q.Namespace, name), pod)
			if pod != nil {
				add(name, pod)
			}
		}
	}
	if len(hostToVirtual) == 0 {
		return q, nil, false, nil
	}
	hostNames := make([]string, 0, len(hostToVirtual))
	for host := range hostToVirtual {
		hostNames = append(hostNames, host)
	}
	slices.Sort(hostNames)
	q.Namespace, q.PodNames = hostNamespace, hostNames
	return q, hostToVirtual, true, nil
}

// queryVClusterWorkload charts a vcluster workload from the host's store and
// keys the answer by the virtual pod names the caller asked with.
func (s *Service) queryVClusterWorkload(ctx context.Context, wp workloadProvider, cluster string, q WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	hostQuery, hostToVirtual, ok, err := s.hostWorkloadQuery(cluster, q)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &WorkloadMetricResponse{Pods: map[string]*MetricResponse{}}, nil
	}
	response, err := wp.QueryWorkloadMetrics(ctx, StoreCluster(cluster), hostQuery)
	if err != nil || response == nil {
		return response, err
	}
	back := make(map[string]*MetricResponse, len(response.Pods))
	for host, data := range response.Pods {
		if virtual, ok := hostToVirtual[host]; ok {
			back[virtual] = data
		}
	}
	response.Pods = back
	return response, nil
}
