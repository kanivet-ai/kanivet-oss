package metrics

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type MetricsServerProvider struct {
	k8s   k8s.Interface
	cache *cache.Cache
}

func NewMetricsServerProvider(k8sClient k8s.Interface, cacheInstance *cache.Cache) *MetricsServerProvider {
	return &MetricsServerProvider{
		k8s:   k8sClient,
		cache: cacheInstance,
	}
}

func (m *MetricsServerProvider) GetName() string {
	return "metrics-server"
}

func (m *MetricsServerProvider) Detect(cluster string) (*ProviderInfo, error) {
	cacheKey := m.cache.BuildKey("metrics-server-info", cluster)

	data, err := m.cache.GetOrSet(cacheKey, 30*time.Minute, func() (interface{}, error) {
		return m.detectInternal(cluster)
	})

	if err != nil {
		return nil, err
	}

	return data.(*ProviderInfo), nil
}

func (m *MetricsServerProvider) detectInternal(cluster string) (*ProviderInfo, error) {
	clientset, err := m.k8s.GetClientForCluster(cluster)
	if err != nil {
		return &ProviderInfo{Type: "metrics-server", Found: false}, nil
	}

	apiGroups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		return &ProviderInfo{Type: "metrics-server", Found: false}, nil
	}

	for _, group := range apiGroups.Groups {
		if group.Name == "metrics.k8s.io" {
			if err := m.verifyConnectivity(cluster); err != nil {
				log.Printf("Metrics Server API found but not working: %v", err)
				return &ProviderInfo{Type: "metrics-server", Found: false}, nil
			}
			return &ProviderInfo{
				Type:    "metrics-server",
				Found:   true,
				Service: "metrics-server",
				URL:     "metrics.k8s.io/v1beta1",
				Version: group.PreferredVersion.Version,
			}, nil
		}
	}

	return &ProviderInfo{Type: "metrics-server", Found: false}, nil
}

func (m *MetricsServerProvider) verifyConnectivity(cluster string) error {
	dynamicClient, err := m.k8s.GetDynamicClient(cluster)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gvr := schema.GroupVersionResource{
		Group:    "metrics.k8s.io",
		Version:  "v1beta1",
		Resource: "nodes",
	}

	_, err = dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1})
	return err
}

func (m *MetricsServerProvider) IsInstalled(cluster string) bool {
	info, err := m.Detect(cluster)
	if err != nil {
		return false
	}
	return info.Found
}

func (m *MetricsServerProvider) Install(cluster string, namespace string) error {
	return fmt.Errorf("metrics-server installation not supported - please install via Helm or kubectl")
}

func (m *MetricsServerProvider) QueryMetrics(cluster string, query MetricQuery) (*MetricResponse, error) {
	dynamicClient, err := m.k8s.GetDynamicClient(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gvr := schema.GroupVersionResource{
		Group:    "metrics.k8s.io",
		Version:  "v1beta1",
		Resource: "pods",
	}

	podMetrics, err := dynamicClient.Resource(gvr).Namespace(query.Namespace).Get(ctx, query.PodName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	containers, found, err := unstructuredNestedSlice(podMetrics.Object, "containers")
	if err != nil || !found {
		return nil, fmt.Errorf("no container metrics found")
	}

	response := &MetricResponse{
		Labels: []string{time.Now().Format("15:04:05")},
		Values: []float64{0},
		Unit:   m.getMetricUnit(query.MetricType),
	}

	var totalValue float64
	for _, c := range containers {
		container, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		containerName, _, _ := unstructuredNestedString(container, "name")
		if query.ContainerName != "" && containerName != query.ContainerName {
			continue
		}

		usage, found, _ := unstructuredNestedMap(container, "usage")
		if !found {
			continue
		}

		switch query.MetricType {
		case "cpu":
			cpuStr, ok := usage["cpu"].(string)
			if ok {
				totalValue += parseCPU(cpuStr)
			}
		case "memory":
			memStr, ok := usage["memory"].(string)
			if ok {
				totalValue += parseMemory(memStr)
			}
		}
	}

	response.Values = []float64{totalValue}
	return response, nil
}

func (m *MetricsServerProvider) getMetricUnit(metricType string) string {
	switch metricType {
	case "cpu":
		return "millicores"
	case "memory":
		return "bytes"
	default:
		return ""
	}
}

func unstructuredNestedSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool, error) {
	val, found, err := nestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, found, err
	}
	s, ok := val.([]interface{})
	return s, ok, nil
}

func unstructuredNestedString(obj map[string]interface{}, fields ...string) (string, bool, error) {
	val, found, err := nestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return "", found, err
	}
	s, ok := val.(string)
	return s, ok, nil
}

func unstructuredNestedMap(obj map[string]interface{}, fields ...string) (map[string]interface{}, bool, error) {
	val, found, err := nestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, found, err
	}
	m, ok := val.(map[string]interface{})
	return m, ok, nil
}

func nestedFieldNoCopy(obj map[string]interface{}, fields ...string) (interface{}, bool, error) {
	var val interface{} = obj
	for _, field := range fields {
		m, ok := val.(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		val, ok = m[field]
		if !ok {
			return nil, false, nil
		}
	}
	return val, true, nil
}

func parseCPU(cpuStr string) float64 {
	var value float64
	if len(cpuStr) > 1 && cpuStr[len(cpuStr)-1] == 'n' {
		fmt.Sscanf(cpuStr[:len(cpuStr)-1], "%f", &value)
		return value / 1000000
	}
	if len(cpuStr) > 1 && cpuStr[len(cpuStr)-1] == 'u' {
		fmt.Sscanf(cpuStr[:len(cpuStr)-1], "%f", &value)
		return value / 1000
	}
	if len(cpuStr) > 1 && cpuStr[len(cpuStr)-1] == 'm' {
		fmt.Sscanf(cpuStr[:len(cpuStr)-1], "%f", &value)
		return value
	}
	fmt.Sscanf(cpuStr, "%f", &value)
	return value * 1000
}

func parseMemory(memStr string) float64 {
	var value float64
	if len(memStr) > 2 && memStr[len(memStr)-2:] == "Ki" {
		fmt.Sscanf(memStr[:len(memStr)-2], "%f", &value)
		return value * 1024
	}
	if len(memStr) > 2 && memStr[len(memStr)-2:] == "Mi" {
		fmt.Sscanf(memStr[:len(memStr)-2], "%f", &value)
		return value * 1024 * 1024
	}
	if len(memStr) > 2 && memStr[len(memStr)-2:] == "Gi" {
		fmt.Sscanf(memStr[:len(memStr)-2], "%f", &value)
		return value * 1024 * 1024 * 1024
	}
	fmt.Sscanf(memStr, "%f", &value)
	return value
}
