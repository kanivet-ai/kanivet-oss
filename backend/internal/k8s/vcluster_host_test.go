package k8s

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func syncedPod(hostNS, hostName, vcName, virtNS, virtName, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hostName,
			Namespace: hostNS,
			Labels:    map[string]string{vclusterManagedByLabel: vcName},
			Annotations: map[string]string{
				vclusterObjectNameAnnotation:      virtName,
				vclusterObjectNamespaceAnnotation: virtNS,
			},
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
}

func TestFindVClusterHostPod_TranslatedName(t *testing.T) {
	client := fake.NewSimpleClientset(
		syncedPod("vc-ns", "freecad-0-x-work-x-vc1", "vc1", "work", "freecad-0", "ip-10-0-1-5"),
	)
	pod, err := findVClusterHostPod(context.Background(), client, "vc-ns", "vc1", "work", "freecad-0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pod.Name != "freecad-0-x-work-x-vc1" || pod.Spec.NodeName != "ip-10-0-1-5" {
		t.Fatalf("got %s on %s", pod.Name, pod.Spec.NodeName)
	}
}

func TestFindVClusterHostPod_HashedNameFallsBackToAnnotations(t *testing.T) {
	client := fake.NewSimpleClientset(
		syncedPod("vc-ns", "some-other-x-work-x-vc1", "vc1", "work", "some-other", "node-a"),
		syncedPod("vc-ns", "very-long-name-3f9a1c", "vc1", "work", "very-long-name", "node-b"),
	)
	pod, err := findVClusterHostPod(context.Background(), client, "vc-ns", "vc1", "work", "very-long-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pod.Spec.NodeName != "node-b" {
		t.Fatalf("expected node-b, got %s", pod.Spec.NodeName)
	}
}

func TestFindVClusterHostPod_IgnoresOtherVClustersInNamespace(t *testing.T) {
	client := fake.NewSimpleClientset(
		syncedPod("shared", "renamed-1", "vc2", "work", "app", "node-other"),
		syncedPod("shared", "renamed-2", "vc1", "work", "app", "node-mine"),
	)
	pod, err := findVClusterHostPod(context.Background(), client, "shared", "vc1", "work", "app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pod.Spec.NodeName != "node-mine" {
		t.Fatalf("expected node-mine, got %s", pod.Spec.NodeName)
	}
}

func TestFindVClusterHostPod_TranslatedNameMustMatchAnnotations(t *testing.T) {
	// A host pod that happens to have the translated name but belongs to a
	// different virtual object must not be returned.
	client := fake.NewSimpleClientset(
		syncedPod("vc-ns", "app-x-work-x-vc1", "vc1", "work", "something-else", "node-a"),
	)
	_, err := findVClusterHostPod(context.Background(), client, "vc-ns", "vc1", "work", "app")
	if !errors.Is(err, ErrVClusterHostPodNotFound) {
		t.Fatalf("expected ErrVClusterHostPodNotFound, got %v", err)
	}
}

func TestFindVClusterHostPod_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	_, err := findVClusterHostPod(context.Background(), client, "vc-ns", "vc1", "work", "missing")
	if !errors.Is(err, ErrVClusterHostPodNotFound) {
		t.Fatalf("expected ErrVClusterHostPodNotFound, got %v", err)
	}
}
