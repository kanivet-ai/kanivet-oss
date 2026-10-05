package k8s

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Metadata the vcluster syncer stamps on every object it copies to the host.
const (
	VClusterManagedByLabel            = "vcluster.loft.sh/managed-by"
	VClusterObjectNameAnnotation      = "vcluster.loft.sh/object-name"
	VClusterObjectNamespaceAnnotation = "vcluster.loft.sh/object-namespace"
	// VClusterOwnerReferencesAnnotation holds the virtual pod's owner references
	// as JSON; the host pod itself is owned by the vcluster's Service.
	VClusterOwnerReferencesAnnotation = "vcluster.loft.sh/owner-references"
)

var ErrVClusterHostPodNotFound = errors.New("host pod not found")

// VClusterHostPod locates the real pod backing a virtual pod, and the real node
// it runs on, in the vcluster's host cluster.
type VClusterHostPod struct {
	Host      string `json:"host"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	NodeName  string `json:"nodeName"`
}

func (c *Client) ResolveVClusterHostPod(vclusterID, namespace, name string) (*VClusterHostPod, error) {
	host, vcNamespace, vcName, ok := parseVClusterID(vclusterID)
	if !ok {
		return nil, fmt.Errorf("not a vcluster: %s", vclusterID)
	}
	client, err := c.GetClientForCluster(host)
	if err != nil {
		return nil, fmt.Errorf("get host client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pod, err := findVClusterHostPod(ctx, client, vcNamespace, vcName, namespace, name)
	if err != nil {
		return nil, err
	}
	return &VClusterHostPod{
		Host:      host,
		Namespace: pod.Namespace,
		Name:      pod.Name,
		NodeName:  pod.Spec.NodeName,
	}, nil
}

// syncedFrom reports whether a host pod is the copy of some pod in the given
// namespace of the given vcluster. Several vclusters can share a host
// namespace; a pod without the managed-by label is taken on its annotations.
func syncedFrom(p *corev1.Pod, vcName, podNamespace string) bool {
	if p.Annotations[VClusterObjectNamespaceAnnotation] != podNamespace {
		return false
	}
	managedBy, labelled := p.Labels[VClusterManagedByLabel]
	return !labelled || managedBy == vcName
}

func findVClusterHostPod(ctx context.Context, client kubernetes.Interface, hostNamespace, vcName, podNamespace, podName string) (*corev1.Pod, error) {
	isMatch := func(p *corev1.Pod) bool {
		return syncedFrom(p, vcName, podNamespace) && p.Annotations[VClusterObjectNameAnnotation] == podName
	}

	// The syncer names host pods <name>-x-<namespace>-x-<vcluster> unless that
	// is too long, in which case it truncates and hashes; try the cheap Get first.
	translated := fmt.Sprintf("%s-x-%s-x-%s", podName, podNamespace, vcName)
	pod, err := client.CoreV1().Pods(hostNamespace).Get(ctx, translated, metav1.GetOptions{})
	if err == nil && isMatch(pod) {
		return pod, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get host pod %s/%s: %w", hostNamespace, translated, err)
	}

	pods, err := client.CoreV1().Pods(hostNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list host pods in %s: %w", hostNamespace, err)
	}
	for i := range pods.Items {
		if isMatch(&pods.Items[i]) {
			return &pods.Items[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %s/%s in vcluster %s", ErrVClusterHostPodNotFound, podNamespace, podName, vcName)
}

// ResolveVClusterHostPods maps virtual pods of one namespace to the host pods
// backing them with a single list, for callers that chart a whole workload.
// Pods with no host pod yet (still pending sync) are left out of the result.
func (c *Client) ResolveVClusterHostPods(vclusterID, namespace string, names []string) (map[string]*VClusterHostPod, error) {
	host, vcNamespace, vcName, ok := parseVClusterID(vclusterID)
	if !ok {
		return nil, fmt.Errorf("not a vcluster: %s", vclusterID)
	}
	client, err := c.GetClientForCluster(host)
	if err != nil {
		return nil, fmt.Errorf("get host client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return findVClusterHostPods(ctx, client, host, vcNamespace, vcName, namespace, names)
}

func findVClusterHostPods(ctx context.Context, client kubernetes.Interface, host, hostNamespace, vcName, podNamespace string, names []string) (map[string]*VClusterHostPod, error) {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[n] = struct{}{}
	}
	pods, err := client.CoreV1().Pods(hostNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list host pods in %s: %w", hostNamespace, err)
	}
	out := make(map[string]*VClusterHostPod, len(names))
	for i := range pods.Items {
		p := &pods.Items[i]
		virtName := p.Annotations[VClusterObjectNameAnnotation]
		if _, ok := want[virtName]; !ok || !syncedFrom(p, vcName, podNamespace) {
			continue
		}
		out[virtName] = &VClusterHostPod{Host: host, Namespace: p.Namespace, Name: p.Name, NodeName: p.Spec.NodeName}
	}
	return out, nil
}
