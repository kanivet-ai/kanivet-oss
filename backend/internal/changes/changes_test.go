package changes

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kanivet/backend/internal/helm"
	"github.com/kanivet/backend/internal/incidents"
)

func tmpl(image string, env ...corev1.EnvVar) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "x" + image}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: image, Env: env}}},
	}
}

func rs(name string, rev string, owner types.UID, at time.Time, t corev1.PodTemplateSpec) appsv1.ReplicaSet {
	return appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "prod", CreationTimestamp: metav1.NewTime(at),
			Labels:          map[string]string{"app": "web"},
			Annotations:     map[string]string{revisionAnnotation: rev},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", UID: owner}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: t},
	}
}

func TestReplicaSetRevisionsDiffAndClassify(t *testing.T) {
	now := time.Now()
	owner := types.UID("dep-uid")
	rss := []appsv1.ReplicaSet{
		rs("web-2", "2", owner, now, tmpl("nginx:1.26", corev1.EnvVar{Name: "MODE", Value: "fast"})),
		rs("web-1", "1", owner, now.Add(-time.Hour), tmpl("nginx:1.25")),
		rs("other", "9", "someone-else", now, tmpl("x")),
	}
	revs := replicaSetRevisions("c", owner, rss)
	if len(revs) != 2 {
		t.Fatalf("want 2 owned revisions, got %d", len(revs))
	}
	es := revisionEntries(ResourceRef{Kind: "Deployment", Name: "web"}, revs, 2)
	last := es[1]
	if !last.Current || last.Revision != "2" {
		t.Fatalf("revision 2 should be current: %+v", last)
	}
	has := func(c Category) bool {
		for _, x := range last.Categories {
			if x == c {
				return true
			}
		}
		return false
	}
	if !has(CatRollout) || !has(CatImage) || !has(CatEnv) {
		t.Fatalf("categories: %v", last.Categories)
	}
	if !strings.Contains(last.Summary, "nginx:1.25 → nginx:1.26") || !strings.Contains(last.Summary, "env MODE") {
		t.Fatalf("summary: %q", last.Summary)
	}
	for _, d := range last.Diff {
		if strings.Contains(d.Path, "pod-template-hash") {
			t.Fatalf("noise in diff: %v", d)
		}
	}
	if !strings.Contains(last.Snapshot, "image: nginx:1.26") || strings.Contains(last.Snapshot, "pod-template-hash") {
		t.Fatalf("snapshot: %s", last.Snapshot)
	}
	if es[0].Categories[0] != CatCreated {
		t.Fatalf("revision 1 is the first rollout: %v", es[0].Categories)
	}
}

func TestControllerRevisionTemplate(t *testing.T) {
	owner := types.UID("sts")
	cr := appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "db-abc", Namespace: "prod", OwnerReferences: []metav1.OwnerReference{{UID: owner}}},
		Revision:   3,
		Data:       runtime.RawExtension{Raw: []byte(`{"spec":{"template":{"$patch":"replace","spec":{"containers":[{"name":"db","image":"pg:16"}]}}}}`)},
	}
	revs := controllerRevisions("c", owner, []appsv1.ControllerRevision{cr})
	if len(revs) != 1 || revs[0].number != 3 {
		t.Fatalf("revs: %+v", revs)
	}
	if _, ok := revs[0].template["$patch"]; ok {
		t.Fatal("$patch kept")
	}
}

func TestManagedFieldsEntries(t *testing.T) {
	ts := metav1.NewTime(time.Now())
	mf := []metav1.ManagedFieldsEntry{
		{Manager: "kubectl-edit", Operation: metav1.ManagedFieldsOperationUpdate, Time: &ts,
			FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:spec":{"f:replicas":{},"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"web\"}":{".":{},"f:image":{}}}}}}}`)}},
		{Manager: "kube-controller-manager", Subresource: "status", Time: &ts, FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:status":{"f:replicas":{}}}`)}},
	}
	es := managedFieldsEntries(ResourceRef{Kind: "Deployment"}, mf)
	if len(es) != 1 {
		t.Fatalf("status writes must be skipped: %d", len(es))
	}
	e := es[0]
	if e.Title != "Edited by kubectl-edit" {
		t.Fatalf("title %q", e.Title)
	}
	joined := strings.Join(e.Fields, ",")
	if !strings.Contains(joined, "spec.replicas") || !strings.Contains(joined, "spec.template.spec.containers[web]") {
		t.Fatalf("fields %v", e.Fields)
	}
	if e.Categories[0] != CatReplicas {
		t.Fatalf("cats %v", e.Categories)
	}
}

func TestManagedPathsKeepDottedKeys(t *testing.T) {
	paths := managedPaths([]byte(`{"f:metadata":{"f:annotations":{"f:meta.helm.sh/release-name":{}}}}`))
	if len(paths) != 1 || paths[0] != "metadata.annotations[meta.helm.sh/release-name]" {
		t.Fatalf("%v", paths)
	}
	if got := condense(paths, 3); got[0] != paths[0] {
		t.Fatalf("condense split a key: %v", got)
	}
}

func TestSecretManagedFieldsListKeysOnly(t *testing.T) {
	ts := metav1.NewTime(time.Now())
	mf := []metav1.ManagedFieldsEntry{{Manager: "kubectl", Time: &ts, FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:password":{},"f:user":{}}}`)}}}
	e := managedFieldsEntries(ResourceRef{Kind: "Secret"}, mf)[0]
	if e.Categories[0] != CatSecret || e.Summary != "owns keys [password, user]" {
		t.Fatalf("%+v", e)
	}
}

func TestRedactManifestSecrets(t *testing.T) {
	m := "apiVersion: v1\nkind: ConfigMap\ndata:\n  a: keep\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: s\ndata:\n  password: aHVudGVyMg==\ntype: Opaque\n"
	out := redactManifestSecrets(m)
	if strings.Contains(out, "aHVudGVyMg==") {
		t.Fatal("secret value leaked")
	}
	if !strings.Contains(out, "a: keep") || !strings.Contains(out, "password: <redacted>") || !strings.Contains(out, "type: Opaque") {
		t.Fatalf("redaction too broad: %s", out)
	}
}

func TestHelmEntries(t *testing.T) {
	now := time.Now()
	es := helmEntries(ResourceRef{}, "ns", "app", []helm.HistoryEntry{
		{Revision: 2, Chart: "app-1.1.0", AppVersion: "2.0", Status: "deployed", Updated: now},
		{Revision: 1, Chart: "app-1.0.0", AppVersion: "1.0", Status: "superseded", Updated: now.Add(-time.Hour)},
	})
	if es[0].Categories[0] != CatCreated || !es[1].Current || !strings.Contains(es[1].Summary, "chart app-1.0.0 → app-1.1.0") {
		t.Fatalf("%+v", es)
	}
	if es[1].ID != "helm:ns/app:2" {
		t.Fatal(es[1].ID)
	}
}

func TestArgoEntries(t *testing.T) {
	app := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "shop", "namespace": "argocd"},
		"status": map[string]interface{}{"history": []interface{}{
			map[string]interface{}{"id": int64(1), "revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "deployedAt": "2026-01-01T00:00:00Z", "source": map[string]interface{}{"repoURL": "r", "path": "p", "targetRevision": "main"}},
			map[string]interface{}{"id": int64(2), "revision": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "deployedAt": "2026-01-02T00:00:00Z", "source": map[string]interface{}{"repoURL": "r", "path": "p", "targetRevision": "main"}},
		}},
	}
	es := argoEntries(ResourceRef{}, app)
	if len(es) != 2 || es[1].Revision != "bbbbbbb" || !es[1].Current || len(es[1].Diff) != 1 {
		t.Fatalf("%+v", es)
	}
}

func TestFluxLastApplied(t *testing.T) {
	obj := map[string]interface{}{
		"kind": "Kustomization", "apiVersion": "kustomize.toolkit.fluxcd.io/v1",
		"metadata": map[string]interface{}{"name": "apps", "namespace": "flux-system"},
		"status": map[string]interface{}{
			"lastAppliedRevision": "main@sha1:0123456789abcdef0123456789abcdef01234567",
			"conditions":          []interface{}{map[string]interface{}{"type": "Ready", "lastTransitionTime": "2026-01-01T00:00:00Z"}},
		},
	}
	es := fluxEntries(ResourceRef{}, obj)
	if len(es) != 1 || es[0].Revision != "main@sha1:0123456" || es[0].Time.IsZero() {
		t.Fatalf("%+v", es)
	}
}

type fakeClients struct {
	typed kubernetes.Interface
	dyn   dynamic.Interface
}

func (f fakeClients) GetClientForCluster(string) (kubernetes.Interface, error) { return f.typed, nil }
func (f fakeClients) GetDynamicClient(string) (dynamic.Interface, error)       { return f.dyn, nil }
func (f fakeClients) GetResourceName(_, _, _, kind string) string {
	return strings.ToLower(kind) + "s"
}

func TestChangesForDeployment(t *testing.T) {
	now := time.Now()
	uid := types.UID("dep")
	dep := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]interface{}{
			"name": "web", "namespace": "prod", "uid": string(uid),
			"creationTimestamp": now.Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"annotations":       map[string]interface{}{revisionAnnotation: "2"},
		},
		"spec": map[string]interface{}{"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "web"}}},
	}}
	r1 := rs("web-1", "1", uid, now.Add(-2*time.Hour), tmpl("nginx:1.25"))
	r2 := rs("web-2", "2", uid, now.Add(-time.Minute), tmpl("nginx:1.26"))
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "e1", Namespace: "prod", UID: "ev1"},
		InvolvedObject: corev1.ObjectReference{Kind: "Deployment", Name: "web", Namespace: "prod"},
		Reason:         "ScalingReplicaSet", Type: "Normal", LastTimestamp: metav1.NewTime(now),
	}
	typed := fake.NewSimpleClientset(&r1, &r2, ev)
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList"}, dep)
	svc := NewService(fakeClients{typed, dyn}, nil)
	h, err := svc.ChangesFor(context.Background(), ResourceRef{Cluster: "c", Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, e := range h.Entries {
		sources = append(sources, string(e.Source))
	}
	got := strings.Join(sources, ",")
	if got != "event,replicaset,replicaset,creation" && got != "event,replicaset,creation,replicaset" {
		t.Fatalf("entries newest first: %s", got)
	}
	if h.Entries[1].Summary != "image web nginx:1.25 → nginx:1.26" {
		t.Fatalf("summary %q", h.Entries[1].Summary)
	}

	feed := deploymentFeed("c", []appsv1.Deployment{{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: uid}}}, []appsv1.ReplicaSet{r1, r2}, now.Add(-time.Hour))
	if len(feed) != 1 || feed[0].Revision != "2" {
		t.Fatalf("feed %+v", feed)
	}
}

func TestIncidentSourceShape(t *testing.T) {
	var _ incidents.ChangeSource = (&Service{}).IncidentSource()
}
