package podcache

import (
	"context"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
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

func newCache(cs kubernetes.Interface) *Cache {
	return New(func(string) (kubernetes.Interface, error) { return cs, nil }, time.Minute)
}

func TestListSyncsAndSlimsPods(t *testing.T) {
	c := newCache(fake.NewSimpleClientset(heavyPod("api-0-x-apps-x-beige-vcluster")))
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pods, err := c.List(ctx, "prod")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
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

func TestListFollowsChanges(t *testing.T) {
	cs := fake.NewSimpleClientset(heavyPod("a"))
	c := newCache(cs)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.List(ctx, "prod"); err != nil {
		t.Fatalf("List: %v", err)
	}

	if _, err := cs.CoreV1().Pods("beige").Create(ctx, heavyPod("b"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		pods, _ := c.List(ctx, "prod")
		if len(pods) == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch did not deliver the new pod, have %d", len(pods))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestIdleClustersAreStopped(t *testing.T) {
	c := newCache(fake.NewSimpleClientset())
	defer c.Close()
	c.Warm("prod")
	c.Warm("staging")
	c.mu.Lock()
	c.entries["prod"].lastUsed = time.Now().Add(-2 * time.Minute)
	c.mu.Unlock()

	c.evictIdle(time.Now())

	c.mu.Lock()
	_, prod := c.entries["prod"]
	_, staging := c.entries["staging"]
	c.mu.Unlock()
	if prod || !staging {
		t.Errorf("prod cached=%v (want false), staging cached=%v (want true)", prod, staging)
	}
}

func TestSlimKeepsOnlyJVMOptions(t *testing.T) {
	p := heavyPod("jvm")
	p.Spec.Containers[0].Env = append(p.Spec.Containers[0].Env, corev1.EnvVar{Name: "JAVA_TOOL_OPTIONS", Value: "-XX:MaxRAMPercentage=75"})
	p.Spec.Containers[0].Command = []string{"/opt/java/bin/java"}
	p.Spec.Containers[0].Args = []string{"-Xmx512m", "-jar", "app.jar", "--password=hunter2"}
	out, _ := slim(p)
	c := out.(*corev1.Pod).Spec.Containers[0]
	if len(c.Env) != 1 || c.Env[0].Name != "JAVA_TOOL_OPTIONS" {
		t.Fatalf("env %v", c.Env)
	}
	if !slices.Equal(c.Args, []string{"/opt/java/bin/java", "-Xmx512m"}) || c.Image != "registry/app:1" {
		t.Fatalf("args %v image %q", c.Args, c.Image)
	}
}
