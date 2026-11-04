package health

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kanivet/backend/internal/db"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) metav1.Time { return metav1.NewTime(now.Add(-d)) }

func ptr[T any](v T) *T { return &v }

func deployPod(name, dep string, cs ...corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app", CreationTimestamp: ago(time.Hour),
			Labels:          map[string]string{"app": dep, "pod-template-hash": "abc12"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: dep + "-abc12", Controller: ptr(true)}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, StartTime: ptr(ago(time.Hour)), ContainerStatuses: cs},
	}
}

func crashing(name string) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, RestartCount: 7, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}
}

func find(rep IssuesReport, rule string) *Issue {
	for i := range rep.Issues {
		if rep.Issues[i].Rule == rule {
			return &rep.Issues[i]
		}
	}
	return nil
}

func TestCrashLoopGroupsByDeployment(t *testing.T) {
	s := &Snapshot{Now: now,
		Deployments: []appsv1.Deployment{{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}},
		Pods:        []*corev1.Pod{deployPod("web-abc12-1", "web", crashing("c")), deployPod("web-abc12-2", "web", crashing("c"))},
	}
	rep := EvaluateIssues(s)
	is := find(rep, "pod.crashloopbackoff")
	if is == nil {
		t.Fatalf("no crashloop issue: %+v", rep.Issues)
	}
	if is.Resource.Kind != "Deployment" || is.Resource.Name != "web" || is.Count != 2 || len(is.Affected) != 2 {
		t.Fatalf("not grouped by owner: %+v", is)
	}
	if is.Severity != SeverityCritical {
		t.Fatalf("severity %s", is.Severity)
	}
}

func TestImagePullAndOOM(t *testing.T) {
	pull := corev1.ContainerStatus{Name: "a", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}}
	oom := corev1.ContainerStatus{Name: "b", RestartCount: 2, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", FinishedAt: ago(5 * time.Minute)}}}
	old := corev1.ContainerStatus{Name: "c", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", FinishedAt: ago(3 * time.Hour)}}}
	s := &Snapshot{Now: now, Pods: []*corev1.Pod{deployPod("p1", "x", pull), deployPod("p2", "y", oom), deployPod("p3", "z", old)}}
	rep := EvaluateIssues(s)
	if find(rep, "pod.imagepullbackoff") == nil {
		t.Fatal("ErrImagePull not reported as ImagePullBackOff")
	}
	n := 0
	for _, is := range rep.Issues {
		if is.Rule == "pod.oomkilled" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one recent OOM, got %d", n)
	}
}

func TestUnschedulableCarriesReason(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "app", CreationTimestamp: ago(10 * time.Minute)},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/3 nodes are available: 3 Insufficient cpu."}}}}
	rep := EvaluateIssues(&Snapshot{Now: now, Pods: []*corev1.Pod{p}})
	is := find(rep, "pod.unschedulable")
	if is == nil || is.Message != "0/3 nodes are available: 3 Insufficient cpu." || is.Severity != SeverityCritical {
		t.Fatalf("got %+v", is)
	}
}

func TestServiceEndpoints(t *testing.T) {
	notReady := deployPod("api-abc12-1", "api")
	notReady.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
	s := &Snapshot{Now: now, Pods: []*corev1.Pod{notReady},
		Services: []corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "app"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "api"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "ghost", Namespace: "app"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "nope"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "headless", Namespace: "app"}},
		},
		Ingresses: []networkingv1.Ingress{{ObjectMeta: metav1.ObjectMeta{Name: "ing", Namespace: "app"},
			Spec: networkingv1.IngressSpec{DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "missing"}}}}},
	}
	rep := EvaluateIssues(s)
	if is := find(rep, "service.no-ready-endpoints"); is == nil || is.Resource.Name != "api" {
		t.Fatalf("no-ready-endpoints: %+v", rep.Issues)
	}
	if is := find(rep, "service.no-pods"); is == nil || is.Resource.Name != "ghost" {
		t.Fatalf("no-pods: %+v", rep.Issues)
	}
	if find(rep, "ingress.missing-service") == nil {
		t.Fatal("ingress backend not reported")
	}
}

func TestUnavailableKindSkipsRules(t *testing.T) {
	s := &Snapshot{Now: now, Unavailable: []string{"Pod"},
		Services: []corev1.Service{{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "x"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"a": "b"}}}}}
	if rep := EvaluateIssues(s); len(rep.Issues) != 0 {
		t.Fatalf("service rule ran without pods: %+v", rep.Issues)
	}
}

func TestRolloutAndNodesAndPVC(t *testing.T) {
	s := &Snapshot{Now: now,
		Deployments: []appsv1.Deployment{{ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "n"}, Spec: appsv1.DeploymentSpec{Replicas: ptr(int32(2))},
			Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Reason: "ProgressDeadlineExceeded"}}}}},
		Nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionUnknown}, {Type: corev1.NodeDiskPressure, Status: corev1.ConditionTrue}}}}},
		PVCs: []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "n", CreationTimestamp: ago(20 * time.Minute)}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending}}},
	}
	rep := EvaluateIssues(s)
	for _, r := range []string{"deployment.rollout-stuck", "node.not-ready", "node.pressure", "pvc.pending"} {
		if find(rep, r) == nil {
			t.Errorf("missing %s", r)
		}
	}
	if rep.Issues[0].Severity != SeverityCritical || rep.Summary.Total != len(rep.Issues) {
		t.Errorf("not ranked: %+v", rep.Issues)
	}
}

func TestJobAndCronJob(t *testing.T) {
	cj := batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "n", CreationTimestamp: ago(30 * 24 * time.Hour)},
		Spec:   batchv1.CronJobSpec{Schedule: "0 2 * * *"},
		Status: batchv1.CronJobStatus{LastScheduleTime: ptr(ago(4 * 24 * time.Hour))}}
	failed := batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "nightly-1", Namespace: "n", CreationTimestamp: ago(4 * 24 * time.Hour),
		OwnerReferences: []metav1.OwnerReference{{Kind: "CronJob", Name: "nightly"}}},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}}}}
	older := failed
	older.Name = "nightly-0"
	older.CreationTimestamp = ago(5 * 24 * time.Hour)
	rep := EvaluateIssues(&Snapshot{Now: now, CronJobs: []batchv1.CronJob{cj}, Jobs: []batchv1.Job{failed, older}})
	if is := find(rep, "cronjob.missed"); is == nil {
		t.Fatalf("missed schedule not reported: %+v", rep.Issues)
	}
	is := find(rep, "job.failed")
	if is == nil || is.Resource.Kind != "CronJob" || is.Count != 1 {
		t.Fatalf("job failure should be on the CronJob, newest run only: %+v", is)
	}
}

func TestEventsFillGapsOnly(t *testing.T) {
	pod := deployPod("web-abc12-1", "web")
	crash := deployPod("api-abc12-1", "api", crashing("c"))
	ev := func(name, reason string) db.K8sEvent {
		return db.K8sEvent{Type: "Warning", Reason: reason, Message: "Readiness probe failed: 500", InvolvedObjectKind: "Pod", InvolvedObjectNamespace: "app",
			InvolvedObjectName: name, LastTimestamp: now.Add(-time.Minute), Count: 3}
	}
	s := &Snapshot{Now: now, Pods: []*corev1.Pod{pod, crash},
		Events: []db.K8sEvent{ev("web-abc12-1", "Unhealthy"), ev("api-abc12-1", "BackOff"), ev("gone", "Unhealthy")}}
	rep := EvaluateIssues(s)
	is := find(rep, "event.unhealthy")
	if is == nil || is.Resource.Kind != "Deployment" || is.Source != "event" {
		t.Fatalf("probe event not surfaced on owner: %+v", rep.Issues)
	}
	if find(rep, "event.backoff") != nil {
		t.Fatal("event duplicated a state issue")
	}
	if len(rep.Issues) != 2 {
		t.Fatalf("want crashloop + unhealthy, got %+v", rep.Issues)
	}
}

func TestCronPeriod(t *testing.T) {
	cases := map[string]time.Duration{
		"*/5 * * * *": 5 * time.Minute, "0 * * * *": time.Hour, "0 2 * * *": 24 * time.Hour,
		"@weekly": 7 * 24 * time.Hour, "30 9 * * 1-5": 72 * time.Hour, "0 0 1 * *": 31 * 24 * time.Hour,
	}
	for spec, want := range cases {
		got, ok := CronPeriod(spec)
		if !ok || got != want {
			t.Errorf("%s: got %v %v want %v", spec, got, ok, want)
		}
	}
	if _, ok := CronPeriod("bogus"); ok {
		t.Error("bogus parsed")
	}
}

func goodDeployment(name string, replicas int32) appsv1.Deployment {
	return appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app"}, Spec: appsv1.DeploymentSpec{Replicas: ptr(replicas),
		Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}}, Spec: corev1.PodSpec{
			AutomountServiceAccountToken: ptr(false),
			SecurityContext:              &corev1.PodSecurityContext{RunAsNonRoot: ptr(true)},
			Containers: []corev1.Container{{Name: "c", Image: "reg/app:1.0",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: {}, corev1.ResourceMemory: {}},
					Limits:   corev1.ResourceList{corev1.ResourceMemory: {}}},
				LivenessProbe: &corev1.Probe{}, ReadinessProbe: &corev1.Probe{},
				SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(false)}}}}}}}
}

func TestAuditCleanWorkloadScores100(t *testing.T) {
	s := &Snapshot{Now: now, Deployments: []appsv1.Deployment{goodDeployment("ok", 2)},
		NetworkPolicies: []networkingv1.NetworkPolicy{{ObjectMeta: metav1.ObjectMeta{Name: "deny", Namespace: "app"}}}}
	s.PDBs = nil
	rep := EvaluateAudit(s, AuditScope{}, nil)
	// Only the PDB check fails for a 2-replica workload without one.
	if len(rep.Findings) != 1 || rep.Findings[0].CheckID != "pdb" {
		t.Fatalf("findings: %+v", rep.Findings)
	}
	if rep.Score >= 100 || rep.Score < 95 {
		t.Fatalf("score %d", rep.Score)
	}
}

func TestAuditFindingsAndIgnores(t *testing.T) {
	bad := goodDeployment("bad", 1)
	c := &bad.Spec.Template.Spec.Containers[0]
	c.Image = "nginx:latest"
	c.SecurityContext = &corev1.SecurityContext{Privileged: ptr(true)}
	bad.Spec.Template.Spec.HostNetwork = true
	bad.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "h", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/run"}}}}
	sys := goodDeployment("coredns", 1)
	sys.Namespace = "kube-system"
	s := &Snapshot{Now: now, Deployments: []appsv1.Deployment{bad, sys}}
	rep := EvaluateAudit(s, AuditScope{}, nil)
	got := map[string]bool{}
	for _, f := range rep.Findings {
		got[f.CheckID] = true
		if f.Resource.Namespace == "kube-system" {
			t.Fatal("system namespace audited by default")
		}
	}
	for _, id := range []string{"privileged", "image-tag", "host-namespaces", "host-path", "single-replica", "network-policy", "privilege-escalation"} {
		if !got[id] {
			t.Errorf("missing finding %s", id)
		}
	}
	if rep.Findings[0].Severity != SeverityCritical {
		t.Errorf("findings not ranked")
	}

	store := NewIgnoreStore(filepath.Join(t.TempDir(), "ig.json"))
	if err := store.Add(Ignore{Cluster: "c", CheckID: "privileged", Namespace: "app", Kind: "Deployment", Name: "bad"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(Ignore{Cluster: "c", CheckID: "network-policy", Namespace: "app"}); err != nil {
		t.Fatal(err)
	}
	rep2 := EvaluateAudit(s, AuditScope{}, NewIgnoreStore(store.path).Matcher("c"))
	if rep2.Score <= rep.Score {
		t.Fatalf("ignores did not raise the score: %d -> %d", rep.Score, rep2.Score)
	}
	ignored := 0
	for _, f := range rep2.Findings {
		if f.Ignored {
			ignored++
		}
	}
	if ignored != 2 {
		t.Fatalf("want 2 ignored findings, got %d", ignored)
	}
}

type stubClients struct{ cs kubernetes.Interface }

func (s stubClients) GetClientForCluster(string) (kubernetes.Interface, error) { return s.cs, nil }

type stubPods []*corev1.Pod

func (p stubPods) List(context.Context, string) ([]*corev1.Pod, error) { return p, nil }

func TestServiceEndToEnd(t *testing.T) {
	d := goodDeployment("web", 1)
	cs := fake.NewSimpleClientset(&d)
	pods := stubPods{deployPod("web-abc12-1", "web", crashing("c"))}
	svc := NewService(NewLoader(stubClients{cs}, pods, nil), nil)
	ctx := context.Background()
	rep, err := svc.Issues(ctx, IssueFilter{Cluster: "c", Severities: []Severity{SeverityCritical}})
	if err != nil || rep.Summary.Critical == 0 {
		t.Fatalf("issues: %+v %v", rep, err)
	}
	rh, err := svc.Resource(ctx, "c", ResourceRef{Kind: "Pod", Namespace: "app", Name: "web-abc12-1"})
	if err != nil || len(rh.Issues) != 1 || len(rh.Findings) == 0 || len(rh.Checks) == 0 {
		t.Fatalf("resource health: %+v %v", rh, err)
	}
	if rh.Findings[0].Resource.Kind != "Deployment" {
		t.Fatalf("pod findings should be its owner's: %+v", rh.Findings[0])
	}
	if _, err := svc.Audit(ctx, AuditScope{}); err == nil {
		t.Fatal("audit without cluster should fail")
	}
}
