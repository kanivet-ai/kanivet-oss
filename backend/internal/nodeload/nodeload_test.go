package nodeload

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type stubPods []*corev1.Pod

func (s stubPods) List(context.Context, string) ([]*corev1.Pod, error) { return s, nil }

type stubUsage struct {
	list *unstructured.UnstructuredList
	err  error
}

func (s stubUsage) ListNodeMetrics(context.Context, string) (*unstructured.UnstructuredList, error) {
	return s.list, s.err
}

func pod(name, node string, phase corev1.PodPhase, cpu, mem string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpu),
					corev1.ResourceMemory: resource.MustParse(mem),
				}},
			}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func nodeMetrics(name, cpu, mem string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": name},
		"usage":    map[string]interface{}{"cpu": cpu, "memory": mem},
	}}
}

var pods = stubPods{
	pod("a", "n1", corev1.PodRunning, "250m", "256Mi"),
	pod("b", "n1", corev1.PodPending, "500m", "1Gi"),
	pod("done", "n1", corev1.PodSucceeded, "4", "8Gi"),
	pod("failed", "n2", corev1.PodFailed, "4", "8Gi"),
	pod("unscheduled", "", corev1.PodPending, "4", "8Gi"),
	pod("c", "n2", corev1.PodRunning, "1", "512Mi"),
}

func TestLoadSumsRequestsOfPodsHoldingTheirReservation(t *testing.T) {
	report, err := NewService(pods, nil).Load(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	n1 := report.Nodes["n1"]
	if n1 == nil || n1.Pods != 2 || n1.CPURequests != 750 || n1.MemoryRequests != (256+1024)<<20 {
		t.Fatalf("n1 = %+v", n1)
	}
	n2 := report.Nodes["n2"]
	if n2 == nil || n2.Pods != 1 || n2.CPURequests != 1000 {
		t.Fatalf("n2 = %+v", n2)
	}
	if len(report.Nodes) != 2 {
		t.Fatalf("nodes = %v, want only n1 and n2", report.Nodes)
	}
	if n1.CPUUsage != nil || report.UsageError != "" {
		t.Fatalf("usage without a source: %+v, %q", n1, report.UsageError)
	}
}

func TestLoadJoinsMetricsServerUsage(t *testing.T) {
	usage := stubUsage{list: &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
		nodeMetrics("n1", "1200m", "2Gi"),
		nodeMetrics("idle", "15m", "300Mi"),
		nodeMetrics("broken", "", "1Gi"),
	}}}
	report, err := NewService(pods, usage).Load(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	n1 := report.Nodes["n1"]
	if n1.CPUUsage == nil || *n1.CPUUsage != 1200 || *n1.MemoryUsage != 2<<30 || n1.Pods != 2 {
		t.Fatalf("n1 = %+v", n1)
	}
	// A node running no pods still gets its usage.
	if idle := report.Nodes["idle"]; idle == nil || idle.CPUUsage == nil || *idle.CPUUsage != 15 || idle.Pods != 0 {
		t.Fatalf("idle = %+v", idle)
	}
	if _, ok := report.Nodes["broken"]; ok {
		t.Fatal("node metrics without a CPU figure should be skipped")
	}
	if report.Nodes["n2"].CPUUsage != nil {
		t.Fatal("n2 has no metrics and should have no usage")
	}
}

func TestLoadReportsRequestsWithoutMetricsServer(t *testing.T) {
	usage := stubUsage{err: errors.New("the server could not find the requested resource")}
	report, err := NewService(pods, usage).Load(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if report.UsageError == "" || report.Nodes["n1"].CPURequests != 750 {
		t.Fatalf("report = %+v", report)
	}
}
