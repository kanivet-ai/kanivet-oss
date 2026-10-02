package finops

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var podMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

const usageTimeout = 5 * time.Second

// fetchPodUsage reads current pod usage from metrics-server, keyed by
// "<namespace>/<name>". It is best effort: clusters without metrics-server,
// or whose aggregated API is broken, return an error and the dashboard falls
// back to request-only figures.
func fetchPodUsage(ctx context.Context, dyn dynamic.Interface, namespace string) (map[string]podUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()
	list, err := dyn.Resource(podMetricsGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return podUsageFromList(list), nil
}

func podUsageFromList(list *unstructured.UnstructuredList) map[string]podUsage {
	out := make(map[string]podUsage, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		containers, _, _ := unstructured.NestedSlice(item.Object, "containers")
		var u podUsage
		for _, c := range containers {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			usage, _, _ := unstructured.NestedStringMap(cm, "usage")
			if q, err := resource.ParseQuantity(usage["cpu"]); err == nil {
				u.cpu += q.MilliValue()
			}
			if q, err := resource.ParseQuantity(usage["memory"]); err == nil {
				u.mem += q.Value()
			}
		}
		out[item.GetNamespace()+"/"+item.GetName()] = u
	}
	return out
}
