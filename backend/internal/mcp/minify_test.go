package mcp

import (
	"encoding/json"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
	"regexp"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mustMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMinifyDropsNoise(t *testing.T) {
	in := mustMap(t, `{
	  "apiVersion":"v1","kind":"Pod",
	  "metadata":{"name":"web","namespace":"prod","uid":"abc","resourceVersion":"9","managedFields":[{"manager":"kubectl"}],
	    "annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{...}","team":"a"},"labels":{}},
	  "spec":{"dnsPolicy":"ClusterFirst","restartPolicy":"Always","schedulerName":"default-scheduler","serviceAccount":"sa","serviceAccountName":"sa",
	    "tolerations":[{"key":"node.kubernetes.io/not-ready"},{"key":"gpu"}],
	    "containers":[{"name":"c","image":"nginx","terminationMessagePath":"/dev/termination-log","terminationMessagePolicy":"File","resources":{}}]},
	  "status":{"phase":"Running","conditions":[{"type":"Ready","status":"True","lastProbeTime":null}]}
	}`)
	out := Minify(in)
	b, _ := json.Marshal(out)
	s := string(b)
	for _, gone := range []string{"managedFields", "uid", "resourceVersion", "last-applied", "dnsPolicy", "restartPolicy", "schedulerName", "terminationMessage", "not-ready", `"serviceAccount":`, "lastProbeTime", `"labels"`, `"resources"`} {
		if strings.Contains(s, gone) {
			t.Errorf("minified output still contains %q: %s", gone, s)
		}
	}
	for _, kept := range []string{`"team":"a"`, `"gpu"`, `"serviceAccountName":"sa"`, `"image":"nginx"`, `"phase":"Running"`} {
		if !strings.Contains(s, kept) {
			t.Errorf("minified output lost %q: %s", kept, s)
		}
	}
	// The input must not be mutated.
	if _, ok := in["metadata"].(map[string]any)["managedFields"]; !ok {
		t.Error("Minify mutated its input")
	}
	if len(s) >= len(renderJSON(in)) {
		t.Error("minified output is not smaller")
	}
}

func TestRedactSecretData(t *testing.T) {
	in := mustMap(t, `{"kind":"Secret","metadata":{"name":"db"},"data":{"password":"aHVudGVyMg=="},"stringData":{"token":"plain"}}`)
	s := renderJSON(Redact(in))
	if strings.Contains(s, "aHVudGVyMg") || strings.Contains(s, "plain") {
		t.Fatalf("secret value leaked: %s", s)
	}
	if !strings.Contains(s, "<redacted 12 bytes>") {
		t.Fatalf("expected size marker: %s", s)
	}
	if in["data"].(map[string]any)["password"] != "aHVudGVyMg==" {
		t.Fatal("Redact mutated its input")
	}
}

func TestRedactEnvAndStrings(t *testing.T) {
	in := mustMap(t, `{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"name":"app","env":[
	  {"name":"DB_PASSWORD","value":"s3cret"},{"name":"LOG_LEVEL","value":"debug"},
	  {"name":"DATABASE_URL","value":"postgres://admin:hunter2@db:5432/app"}]}]}}}}`)
	s := renderJSON(Redact(in))
	if strings.Contains(s, "s3cret") || strings.Contains(s, "hunter2") {
		t.Fatalf("leak: %s", s)
	}
	if !strings.Contains(s, "debug") || !strings.Contains(s, "postgres://admin:<redacted>@db:5432/app") {
		t.Fatalf("over-redacted: %s", s)
	}
}

func TestScrubText(t *testing.T) {
	cases := map[string]string{
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz123": "abcdefghijklmnop",
		"key AKIAABCDEFGHIJKLMNOP used":                       "AKIAABCDEFGHIJKLMNOP",
		"token=ghp_abcdefghijklmnopqrstuvwxyz0123":            "ghp_abcdefghij",
		`{"password":"hunter2","user":"bob"}`:                 "hunter2",
		"password: hunter2":                                   "hunter2",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U": "eyJzdWIi",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----":                           "MIIEow",
		"api_key=sk-abcdefghijklmnopqrstuvwxyz":                                                            "sk-abcdefghij",
	}
	for in, secret := range cases {
		got := ScrubText(in)
		if strings.Contains(got, secret) {
			t.Errorf("ScrubText(%q) = %q still contains %q", in, got, secret)
		}
	}
	plain := "GET /healthz 200 in 3ms user=bob"
	if ScrubText(plain) != plain {
		t.Errorf("plain line changed: %q", ScrubText(plain))
	}
}

func TestSummarizePod(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	pod := mustMap(t, `{"kind":"Pod","metadata":{"name":"p","namespace":"n","creationTimestamp":"2026-01-01T10:00:00Z"},
	  "spec":{"nodeName":"node-1"},
	  "status":{"phase":"Running","containerStatuses":[{"ready":false,"restartCount":7,"state":{"waiting":{"reason":"CrashLoopBackOff"}}},{"ready":true,"restartCount":0,"state":{"running":{}}}]}}`)
	r := Summarize(pod, now)
	if r.Status != "CrashLoopBackOff" || r.Ready != "1/2" || r.Restarts != 7 || r.Node != "node-1" || r.Age != "2h" {
		t.Fatalf("unexpected row %+v", r)
	}
}

func TestDedupeEvents(t *testing.T) {
	now := time.Now()
	mk := func(name, msg string, count int32, ago time.Duration) corev1.Event {
		return corev1.Event{
			Type: "Warning", Reason: "BackOff", Message: msg, Count: count,
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "n", Name: name},
			FirstTimestamp: metav1.NewTime(now.Add(-ago)), LastTimestamp: metav1.NewTime(now.Add(-ago)),
		}
	}
	evs := []corev1.Event{
		mk("p1", "Back-off restarting failed container app in pod p1_n(1234abcd)", 3, time.Hour),
		mk("p1", "Back-off restarting failed container app in pod p1_n(9876fedc)", 2, time.Minute),
		mk("p2", "Back-off restarting failed container app", 1, 2*time.Hour),
	}
	g := DedupeEvents(evs, now)
	if len(g) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(g), g)
	}
	if g[0].Object != "Pod/n/p1" || g[0].Count != 5 {
		t.Fatalf("unexpected first group %+v", g[0])
	}
}

func TestFilterLogLines(t *testing.T) {
	text := "start\nGET / 200\nGET / 200\nGET / 200\nERROR db password=hunter2 refused\nok\n"
	lines, _ := FilterLogLines(text, nil, false, 0)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "repeated 2 more times") || strings.Contains(joined, "hunter2") {
		t.Fatalf("unexpected: %s", joined)
	}
	lines, dropped := FilterLogLines(text, nil, true, 0)
	if len(lines) != 1 || dropped != 5 {
		t.Fatalf("errorsOnly: %v dropped=%d", lines, dropped)
	}
	lines, _ = FilterLogLines(text, regexp.MustCompile("(?i)^get"), false, 1)
	if len(lines) != 1 {
		t.Fatalf("grep+max: %v", lines)
	}
}

func TestRedactManifest(t *testing.T) {
	m := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\ndata:\n  k: c2VjcmV0\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\ndata:\n  a: b\n"
	out := RedactManifest(m)
	if strings.Contains(out, "c2VjcmV0") || !strings.Contains(out, "ConfigMap") {
		t.Fatalf("unexpected manifest: %s", out)
	}
}

func renderJSON(v any) string {
	s, _ := mcpreg.Render(v)
	return s
}
