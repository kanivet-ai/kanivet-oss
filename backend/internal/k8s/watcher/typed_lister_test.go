package watcher

import (
	"context"
	jsonv2 "encoding/json/v2"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s/watcher/listadapters"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
)

var slimTime = metav1.NewTime(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

func slimMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: name, Namespace: "ns", UID: types.UID("uid-" + name), ResourceVersion: "7", Generation: 3,
		CreationTimestamp: slimTime,
		Labels:            map[string]string{"app": name},
		Annotations:       map[string]string{"deployment.kubernetes.io/revision": "4", "team": "core"},
		OwnerReferences:   []metav1.OwnerReference{{APIVersion: "v1", Kind: "Thing", Name: "owner", UID: "owner-uid"}},
		ManagedFields: []metav1.ManagedFieldsEntry{{
			Manager: "kubectl", Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1", Time: &slimTime,
			FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:app":{}}},"f:spec":{"f:replicas":{}}}`)},
		}},
	}
}

func slimPodTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "x"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app", Image: "registry.example.com/app:1.0",
			Env:       []corev1.EnvVar{{Name: "A", Value: "1"}},
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}},
		}}},
	}
}

func int32p(v int32) *int32 { return &v }

// slimFixtures holds one object of every native kind whose typed list goes
// through slimTyped, with the fields slimming drops filled in.
func slimFixtures() map[schema.GroupVersionResource]runtime.Object {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}}
	cond := []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue, LastUpdateTime: slimTime, LastTransitionTime: slimTime, Reason: "MinimumReplicasAvailable"}}
	return map[schema.GroupVersionResource]runtime.Object{
		{Group: "apps", Version: "v1", Resource: "deployments"}: &appsv1.Deployment{
			ObjectMeta: slimMeta("deploy"),
			Spec:       appsv1.DeploymentSpec{Replicas: int32p(3), Selector: sel, Template: slimPodTemplate()},
			Status:     appsv1.DeploymentStatus{Replicas: 3, ReadyReplicas: 2, UpdatedReplicas: 3, AvailableReplicas: 2, Conditions: cond},
		},
		{Group: "apps", Version: "v1", Resource: "replicasets"}: &appsv1.ReplicaSet{
			ObjectMeta: slimMeta("rs"),
			Spec:       appsv1.ReplicaSetSpec{Replicas: int32p(2), Selector: sel, Template: slimPodTemplate()},
			Status:     appsv1.ReplicaSetStatus{Replicas: 2, ReadyReplicas: 2, AvailableReplicas: 2},
		},
		{Group: "apps", Version: "v1", Resource: "statefulsets"}: &appsv1.StatefulSet{
			ObjectMeta: slimMeta("sts"),
			Spec: appsv1.StatefulSetSpec{Replicas: int32p(1), Selector: sel, Template: slimPodTemplate(),
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "data"}}}},
			Status: appsv1.StatefulSetStatus{Replicas: 1, ReadyReplicas: 1},
		},
		{Group: "apps", Version: "v1", Resource: "daemonsets"}: &appsv1.DaemonSet{
			ObjectMeta: slimMeta("ds"),
			Spec:       appsv1.DaemonSetSpec{Selector: sel, Template: slimPodTemplate()},
			Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 4, CurrentNumberScheduled: 4, NumberReady: 3, NumberAvailable: 3},
		},
		{Group: "batch", Version: "v1", Resource: "jobs"}: &batchv1.Job{
			ObjectMeta: slimMeta("job"),
			Spec:       batchv1.JobSpec{Completions: int32p(1), Parallelism: int32p(1), BackoffLimit: int32p(6), Template: slimPodTemplate()},
			Status:     batchv1.JobStatus{Succeeded: 1, StartTime: &slimTime, CompletionTime: &slimTime},
		},
		{Group: "batch", Version: "v1", Resource: "cronjobs"}: &batchv1.CronJob{
			ObjectMeta: slimMeta("cron"),
			Spec:       batchv1.CronJobSpec{Schedule: "*/5 * * * *", JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: slimPodTemplate()}}},
			Status:     batchv1.CronJobStatus{LastScheduleTime: &slimTime},
		},
		{Version: "v1", Resource: "replicationcontrollers"}: &corev1.ReplicationController{
			ObjectMeta: slimMeta("rc"),
			Spec:       corev1.ReplicationControllerSpec{Replicas: int32p(1), Template: func() *corev1.PodTemplateSpec { t := slimPodTemplate(); return &t }()},
			Status:     corev1.ReplicationControllerStatus{Replicas: 1},
		},
		{Version: "v1", Resource: "nodes"}: &corev1.Node{
			ObjectMeta: slimMeta("node"),
			Spec:       corev1.NodeSpec{Unschedulable: true, Taints: []corev1.Taint{{Key: "k", Effect: corev1.TaintEffectNoSchedule}}},
			Status: corev1.NodeStatus{
				Capacity:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: slimTime}},
				Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.1"}},
				NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.31.0"},
				Images:     []corev1.ContainerImage{{Names: []string{"registry.example.com/app:1.0"}, SizeBytes: 1 << 20}},
			},
		},
		{Version: "v1", Resource: "secrets"}: &corev1.Secret{
			ObjectMeta: slimMeta("secret"), Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"password": []byte("hunter2"), "user": []byte("admin")},
		},
		{Version: "v1", Resource: "configmaps"}: &corev1.ConfigMap{
			ObjectMeta: slimMeta("cm"),
			Data:       map[string]string{"a": "1", "b": "2"},
			BinaryData: map[string][]byte{"bin": {1, 2, 3}},
		},
		{Version: "v1", Resource: "services"}: &corev1.Service{
			ObjectMeta: slimMeta("svc"),
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.1.0.1", Selector: map[string]string{"app": "x"}, Ports: []corev1.ServicePort{{Name: "http", Port: 80}}},
		},
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}: &networkingv1.Ingress{
			ObjectMeta: slimMeta("ing"),
			Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "example.com"}}},
		},
		{Version: "v1", Resource: "namespaces"}: &corev1.Namespace{
			ObjectMeta: slimMeta("team"),
			Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
	}
}

func rowJSON(t *testing.T, u *unstructured.Unstructured, gvr schema.GroupVersionResource) string {
	t.Helper()
	b, err := jsonv2.Marshal(listadapters.Simplify(u, gvr), jsonv2.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Slimming before the reflection conversion must not change a single list
// row: the reference is the same typed list converted whole, as before.
func TestTypedListSlimmingKeepsRowsIdentical(t *testing.T) {
	ctx := context.Background()
	for gvr, obj := range slimFixtures() {
		t.Run(gvr.Resource, func(t *testing.T) {
			client := fake.NewClientset(obj)
			raw, err := typedList(ctx, client, gvr, "", metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want, err := typedListToUnstructured(raw)
			if err != nil {
				t.Fatal(err)
			}

			got, err := newTypedLister(client, gvr).List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}

			if len(got.Items) != 1 || len(want.Items) != 1 {
				t.Fatalf("got %d items, want 1", len(got.Items))
			}
			if mf, _, _ := unstructured.NestedFieldNoCopy(got.Items[0].Object, "metadata", "managedFields"); mf != nil {
				t.Fatal("managedFields were converted")
			}
			if g, w := rowJSON(t, &got.Items[0], gvr), rowJSON(t, &want.Items[0], gvr); g != w {
				t.Fatalf("slimming changed the row\n got: %s\nwant: %s", g, w)
			}
		})
	}
}

func TestTypedWatchAdapterSlimsBeforeConverting(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	d := slimFixtures()[gvr].(*appsv1.Deployment)
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(d.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	want := rowJSON(t, &unstructured.Unstructured{Object: m}, gvr)

	inner := watch.NewFake()
	a := newTypedWatchAdapter(inner)
	defer a.Stop()
	go inner.Modify(d)

	select {
	case ev := <-a.ResultChan():
		u, ok := ev.Object.(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("watch event not converted: %T", ev.Object)
		}
		if c, _, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "template", "spec", "containers"); c != nil {
			t.Fatal("pod template was converted")
		}
		if got := rowJSON(t, u, gvr); got != want {
			t.Fatalf("slimming changed the row\n got: %s\nwant: %s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("no event from the watch adapter")
	}
}
