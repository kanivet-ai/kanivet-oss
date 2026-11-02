// Package nodeload reports how loaded each node is, for the Nodes list: what
// its pods request, how many pods it runs, and what metrics-server says it
// uses. Allocatable is not repeated here; the list rows already carry it.
package nodeload

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kanivet/backend/internal/finops"
)

// Load is one node's figures. CPU is in millicores and memory in bytes.
// Usage is nil when metrics-server has no figures for the node.
type Load struct {
	CPURequests    int64  `json:"cpuRequests"`
	MemoryRequests int64  `json:"memoryRequests"`
	Pods           int    `json:"pods"`
	CPUUsage       *int64 `json:"cpuUsage,omitempty"`
	MemoryUsage    *int64 `json:"memoryUsage,omitempty"`
}

// Report is every node's load, keyed by node name.
type Report struct {
	Nodes map[string]*Load `json:"nodes"`
	// UsageError says why usage is missing, typically that the cluster has
	// no metrics-server. Requests and pod counts are still reported.
	UsageError string `json:"usageError,omitempty"`
}

// PodLister serves the cluster's pods; the shared pod cache in practice.
type PodLister interface {
	List(ctx context.Context, cluster string) ([]*corev1.Pod, error)
}

// UsageSource lists metrics-server's node metrics.
type UsageSource interface {
	ListNodeMetrics(ctx context.Context, cluster string) (*unstructured.UnstructuredList, error)
}

type Service struct {
	pods  PodLister
	usage UsageSource
}

func NewService(pods PodLister, usage UsageSource) *Service {
	return &Service{pods: pods, usage: usage}
}

// Load sums the requests of the pods on each node and joins metrics-server's
// usage. Usage is best effort: without it the report still has requests.
func (s *Service) Load(ctx context.Context, cluster string) (*Report, error) {
	var (
		wg       sync.WaitGroup
		usage    *unstructured.UnstructuredList
		usageErr error
	)
	if s.usage != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			usage, usageErr = s.usage.ListNodeMetrics(ctx, cluster)
		}()
	}
	pods, err := s.pods.List(ctx, cluster)
	wg.Wait()
	if err != nil {
		return nil, err
	}

	report := &Report{Nodes: requestsByNode(pods)}
	if usageErr != nil {
		report.UsageError = usageErr.Error()
	} else if usage != nil {
		addUsage(report, usage)
	}
	return report, nil
}

// requestsByNode counts the pods bound to each node that still hold their
// reservation, as `kubectl describe node` does: finished pods do not.
func requestsByNode(pods []*corev1.Pod) map[string]*Load {
	out := map[string]*Load{}
	for _, p := range pods {
		if p.Spec.NodeName == "" || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		l := out[p.Spec.NodeName]
		if l == nil {
			l = &Load{}
			out[p.Spec.NodeName] = l
		}
		cpu, mem := finops.PodRequests(&p.Spec)
		l.CPURequests += cpu
		l.MemoryRequests += mem
		l.Pods++
	}
	return out
}

func addUsage(report *Report, list *unstructured.UnstructuredList) {
	for i := range list.Items {
		item := &list.Items[i]
		usage, _, _ := unstructured.NestedStringMap(item.Object, "usage")
		cpu, cpuErr := resource.ParseQuantity(usage["cpu"])
		mem, memErr := resource.ParseQuantity(usage["memory"])
		if cpuErr != nil || memErr != nil {
			continue
		}
		l := report.Nodes[item.GetName()]
		if l == nil {
			l = &Load{}
			report.Nodes[item.GetName()] = l
		}
		cpuMilli, memBytes := cpu.MilliValue(), mem.Value()
		l.CPUUsage = &cpuMilli
		l.MemoryUsage = &memBytes
	}
}
