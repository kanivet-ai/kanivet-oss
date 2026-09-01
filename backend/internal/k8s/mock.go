package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
)

var _ Interface = (*MockClient)(nil)

type MockClient struct {
	TypedClient    kubernetes.Interface
	DynamicClient  dynamic.Interface
	MetadataClient metadata.Interface
	ClusterName    string
	ClusterVersion string
}

func (m *MockClient) ListClusters() ([]ClusterInfo, error) {
	name := m.ClusterName
	if name == "" {
		name = "test-cluster"
	}
	return []ClusterInfo{{Name: name}}, nil
}

func (m *MockClient) GetClientForCluster(cluster string) (kubernetes.Interface, error) {
	if m.TypedClient == nil {
		return nil, fmt.Errorf("no typed client configured for cluster %s", cluster)
	}
	return m.TypedClient, nil
}

func (m *MockClient) GetClientAndConfig(cluster string) (kubernetes.Interface, *rest.Config, error) {
	client, err := m.GetClientForCluster(cluster)
	if err != nil {
		return nil, nil, err
	}
	return client, &rest.Config{Host: "https://mock:6443"}, nil
}

func (m *MockClient) GetDynamicClient(cluster string) (dynamic.Interface, error) {
	if m.DynamicClient == nil {
		return nil, fmt.Errorf("no dynamic client configured for cluster %s", cluster)
	}
	return m.DynamicClient, nil
}

func (m *MockClient) GetInteractiveDynamicClient(cluster string) (dynamic.Interface, error) {
	return m.GetDynamicClient(cluster)
}

func (m *MockClient) GetBulkMetadataClient(cluster string) (metadata.Interface, error) {
	return m.GetMetadataClient(cluster)
}

func (m *MockClient) GetMetadataClient(cluster string) (metadata.Interface, error) {
	if m.MetadataClient == nil {
		return nil, fmt.Errorf("no metadata client configured for cluster %s", cluster)
	}
	return m.MetadataClient, nil
}

func (m *MockClient) GetDiscoveryClient(cluster string) (discovery.DiscoveryInterface, error) {
	if m.TypedClient == nil {
		return nil, fmt.Errorf("no discovery client configured for cluster %s", cluster)
	}
	return m.TypedClient.Discovery(), nil
}

func (m *MockClient) ResolveKindToResource(cluster, group, version, kind string) (string, error) {
	return fmt.Errorf("not implemented in mock").Error(), fmt.Errorf("not implemented in mock")
}

func (m *MockClient) GetResourceName(cluster, group, version, kind string) string {
	return kind
}

func (m *MockClient) ListAPIResources(cluster string) ([]metav1.APIResource, error) {
	return nil, nil
}

func (m *MockClient) GetClusterStatus(cluster string) (*ClusterStatus, error) {
	version := m.ClusterVersion
	if version == "" {
		version = "1.30"
	}
	return &ClusterStatus{
		Name:    cluster,
		Healthy: true,
		Version: version,
	}, nil
}

func (m *MockClient) ListNamespaces(cluster string) ([]string, error) {
	client, err := m.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	nsList, err := client.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	result := make([]string, len(nsList.Items))
	for i, ns := range nsList.Items {
		result[i] = ns.Name
	}
	return result, nil
}

func (m *MockClient) ScaleResource(ctx context.Context, cluster, group, version, kind, namespace, name string, replicas int32) error {
	return fmt.Errorf("not implemented in mock")
}

func (m *MockClient) TaintNode(ctx context.Context, cluster, nodeName string, key, value, effect string) error {
	return fmt.Errorf("not implemented in mock")
}

func (m *MockClient) RemoveTaint(ctx context.Context, cluster, nodeName, key string) error {
	return fmt.Errorf("not implemented in mock")
}

func (m *MockClient) DrainNode(ctx context.Context, cluster, nodeName string, ignoreDaemonsets, deleteEmptyDir bool, gracePeriod int) (*DrainResult, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *MockClient) CordonNode(ctx context.Context, cluster, nodeName string, unschedulable bool) error {
	return fmt.Errorf("not implemented in mock")
}

func (m *MockClient) RestartResource(ctx context.Context, cluster, group, version, kind, namespace, name string) error {
	return fmt.Errorf("not implemented in mock")
}

func (m *MockClient) TriggerCronJob(ctx context.Context, cluster, namespace, name string) (string, error) {
	return "", fmt.Errorf("not implemented in mock")
}

func (m *MockClient) GetRolloutStatus(ctx context.Context, cluster, group, version, kind, namespace, name string) (*RolloutStatus, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *MockClient) GetBatchRolloutStatus(ctx context.Context, cluster string, requests []RolloutStatusRequest) []BatchRolloutStatus {
	return nil
}

func (m *MockClient) GetResourceCount(ctx context.Context, cluster string, gvr schema.GroupVersionResource) (int, error) {
	if m.TypedClient == nil {
		return 0, fmt.Errorf("no client configured")
	}
	dynClient, err := m.GetDynamicClient(cluster)
	if err != nil {
		return 0, err
	}
	list, err := dynClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, nil
	}
	return len(list.Items), nil
}

func (m *MockClient) UpdateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *MockClient) CreateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *MockClient) CreatePortForward(cluster, namespace, podName string, remotePort int) (*PortForward, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *MockClient) StopPortForward(id string) error {
	return fmt.Errorf("not implemented in mock")
}

func (m *MockClient) GetPortForward(id string) (*PortForward, bool) {
	return nil, false
}

func (m *MockClient) GetPodLogs(ctx context.Context, cluster, namespace, name, container, tailLines string) (string, error) {
	return "", fmt.Errorf("not implemented in mock")
}

func (m *MockClient) GetCrossplaneXRDs(cluster string) ([]map[string]interface{}, error) {
	return nil, nil
}

func (m *MockClient) IsCrossplaneResource(cluster string, group string, version string, kind string) (bool, error) {
	return false, nil
}

func (m *MockClient) RefreshClusterCache(cluster string) {}

func (m *MockClient) ListVClusters(host string) ([]VClusterInfo, error) {
	return nil, nil
}

func (m *MockClient) ConnectVCluster(host, namespace, name string) (*VClusterConnection, error) {
	return nil, fmt.Errorf("vcluster not supported in mock")
}

func (m *MockClient) DisconnectVCluster(id string) error { return nil }
