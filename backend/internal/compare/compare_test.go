package compare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func deployment(name string, replicas int64, image string, env map[string]string) map[string]any {
	var envList []any
	for k, v := range env {
		envList = append(envList, map[string]any{"name": k, "value": v})
	}
	c := map[string]any{
		"name": "app", "image": image,
		"terminationMessagePath": "/dev/termination-log", "terminationMessagePolicy": "File",
		"ports":     []any{map[string]any{"containerPort": int64(8080), "protocol": "TCP"}},
		"resources": map[string]any{"requests": map[string]any{"cpu": "100m"}},
	}
	if envList != nil {
		c["env"] = envList
	}
	return map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{
			"name": name, "namespace": "default", "uid": "u-" + name, "resourceVersion": "42",
			"creationTimestamp": "2026-01-01T00:00:00Z", "generation": int64(3),
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
			"annotations":   map[string]any{"deployment.kubernetes.io/revision": "7"},
			"labels":        map[string]any{"app": "web"},
		},
		"spec": map[string]any{
			"replicas": replicas, "progressDeadlineSeconds": int64(600), "revisionHistoryLimit": int64(10),
			"template": map[string]any{"spec": map[string]any{
				"dnsPolicy": "ClusterFirst", "restartPolicy": "Always", "schedulerName": "default-scheduler",
				"securityContext": map[string]any{}, "terminationGracePeriodSeconds": int64(30),
				"containers": []any{c},
			}},
		},
		"status": map[string]any{"readyReplicas": replicas},
	}
}

func titles(in []Insight) string {
	var t []string
	for _, i := range in {
		t = append(t, i.Title)
	}
	return strings.Join(t, " | ")
}

func TestNormalizeStripsServerFields(t *testing.T) {
	d := deployment("a", 2, "nginx:1", nil)
	Normalize(d, Options{})
	y, _ := ToYAML(d)
	for _, bad := range []string{"uid", "resourceVersion", "managedFields", "creationTimestamp", "status", "dnsPolicy", "terminationMessagePath", "revision", "progressDeadlineSeconds", "protocol"} {
		if strings.Contains(y, bad) {
			t.Errorf("normalized YAML still contains %q:\n%s", bad, y)
		}
	}
	if !strings.HasPrefix(y, "apiVersion: apps/v1\nkind: Deployment\nmetadata:") {
		t.Errorf("YAML not in kubectl order:\n%s", y)
	}
}

func TestNormalizeKeepsStatusWhenAsked(t *testing.T) {
	d := deployment("a", 2, "nginx:1", nil)
	Normalize(d, Options{IncludeStatus: true, IncludeMetadata: true})
	if d["status"] == nil || mapAt(d, "metadata")["uid"] == nil {
		t.Fatal("status/metadata stripped although requested")
	}
}

func TestSemanticSummary(t *testing.T) {
	a := deployment("web", 2, "nginx:1.25", map[string]string{"FOO": "1", "MODE": "a"})
	b := deployment("web", 5, "nginx:1.27", map[string]string{"MODE": "b"})
	mapAt(b, "spec", "template", "spec")["containers"].([]any)[0].(map[string]any)["resources"] =
		map[string]any{"requests": map[string]any{"cpu": "500m"}}
	res, err := CompareObjects(a, b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := titles(res.Summary)
	for _, want := range []string{"Replicas differ", "Image differs (app)", "Env FOO only in A (app)", "Env MODE differs (app)", "Resources differ (app)"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q; got %s", want, got)
		}
	}
	for _, in := range res.Summary {
		if in.Title == "Replicas differ" && (in.A != "2" || in.B != "5") {
			t.Errorf("replicas = %s vs %s", in.A, in.B)
		}
		if strings.HasPrefix(in.Title, "status") {
			t.Errorf("status should be hidden: %s", in.Title)
		}
	}
	if res.Identical || res.Stats.Changed == 0 {
		t.Errorf("stats = %+v", res.Stats)
	}
}

func TestIdenticalAfterNormalization(t *testing.T) {
	a := deployment("web", 2, "nginx:1", nil)
	b := deployment("web", 2, "nginx:1", nil)
	mapAt(b, "metadata")["uid"] = "other"
	mapAt(b, "metadata")["resourceVersion"] = "99"
	res, _ := CompareObjects(a, b, Options{})
	if !res.Identical {
		t.Fatalf("expected identical, got %+v", res.Changes)
	}
}

func TestSecretRedaction(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	a := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "s"},
		"data": map[string]any{"user": enc("admin"), "pass": enc("hunter2"), "only-a": enc("x")}}
	b := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "s"},
		"data": map[string]any{"user": enc("admin"), "pass": enc("hunter3")}, "stringData": map[string]any{"token": "t"}}
	res, _ := CompareObjects(a, b, Options{})
	all, _ := json.Marshal(res)
	for _, secret := range []string{"hunter2", "hunter3", enc("hunter2"), enc("admin"), "admin\""} {
		if strings.Contains(string(all), secret) {
			t.Fatalf("secret value %q leaked: %s", secret, all)
		}
	}
	got := titles(res.Summary)
	for _, want := range []string{"Keys only in A: only-a", "Keys only in B: token", "Values differ: pass"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q; got %s", want, got)
		}
	}
	for _, c := range res.Changes {
		if strings.Contains(c.Path, "user") {
			t.Errorf("equal values must hash equal: %+v", c)
		}
	}
}

func TestLastAppliedAnnotationNeverLeaks(t *testing.T) {
	a := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "s",
		"annotations": map[string]any{lastAppliedAnnotation: `{"data":{"k":"c2VjcmV0"}}`}}}
	Normalize(a, Options{IncludeMetadata: true})
	y, _ := ToYAML(a)
	if strings.Contains(y, "c2VjcmV0") {
		t.Fatal("last-applied leaked secret data")
	}
}

func TestPruneUndeclared(t *testing.T) {
	declared := map[string]any{"spec": map[string]any{"replicas": float64(2), "template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "app", "image": "nginx:1"}}}}}}
	live := map[string]any{"spec": map[string]any{"replicas": int64(3), "paused": false, "template": map[string]any{"spec": map[string]any{
		"containers": []any{
			map[string]any{"name": "app", "image": "nginx:1", "imagePullPolicy": "IfNotPresent"},
			map[string]any{"name": "sidecar", "image": "envoy"},
		}}}}}
	got := PruneUndeclared(live, declared)
	ch := Diff(declared, got)
	if len(ch) != 2 {
		t.Fatalf("want replicas change and sidecar added, got %+v", ch)
	}
	if ch[0].Path != "spec.replicas" || ch[1].Path != "spec.template.spec.containers[sidecar]" || ch[1].Type != OnlyInB {
		t.Fatalf("unexpected changes %+v", ch)
	}
}

func TestFindInManifest(t *testing.T) {
	m := "---\n# Source: chart/templates/svc.yaml\napiVersion: v1\nkind: Service\nmetadata:\n  name: web\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replicas: 2\n"
	obj := FindInManifest(m, "Deployment", "web", "prod")
	if obj == nil || mapAt(obj, "spec")["replicas"].(float64) != 2 {
		t.Fatalf("got %v", obj)
	}
	if FindInManifest(m, "Deployment", "api", "prod") != nil {
		t.Fatal("matched wrong name")
	}
}

func TestPathsQuoteDottedKeys(t *testing.T) {
	a := map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": "a"}}}
	b := map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": "b"}}}
	ch := Diff(a, b)
	if len(ch) != 1 || ch[0].Path != `metadata.labels["app.kubernetes.io/name"]` {
		t.Fatalf("got %+v", ch)
	}
	if section(ch[0].Path) != "metadata.labels" {
		t.Fatalf("section = %s", section(ch[0].Path))
	}
}

func TestServiceDriftAndSideErrors(t *testing.T) {
	live := deployment("web", 5, "nginx:1.27", nil)
	applied := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "web", "namespace": "default"},
		"spec": map[string]any{"replicas": 2, "template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": "nginx:1.27"}}}}}}
	raw, _ := json.Marshal(applied)
	mapAt(live, "metadata")["annotations"].(map[string]any)[lastAppliedAnnotation] = string(raw)

	s := NewService(nil, nil)
	s.fetchLive = func(_ context.Context, r Ref) (map[string]any, error) {
		if r.Cluster == "prod" {
			return nil, &SideError{Status: http.StatusNotFound, Err: errString("Deployment web not found in prod")}
		}
		// Each call gets its own copy, as a real GET would.
		b, _ := json.Marshal(live)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m, nil
	}
	ref := Ref{Cluster: "dev", Version: "v1", Group: "apps", Kind: "Deployment", Namespace: "default", Name: "web"}

	bl, err := s.Baselines(context.Background(), ref)
	if err != nil || !bl.LastApplied || bl.HelmRelease != "" {
		t.Fatalf("baselines = %+v, %v", bl, err)
	}

	res, err := s.Drift(context.Background(), ref, SourceLastApplied, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 || res.Changes[0].Path != "spec.replicas" {
		t.Fatalf("drift should only show replicas, got %+v", res.Changes)
	}

	if _, err := s.Drift(context.Background(), ref, SourceHelm, Options{}); err != nil {
		t.Fatal(err)
	}
	other := ref
	other.Cluster = "prod"
	res, _ = s.Compare(context.Background(), ref, other, Options{})
	if res.B.Status != http.StatusNotFound || res.A.Error != "" || len(res.Changes) != 0 {
		t.Fatalf("side error not reported: %+v", res.B)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
