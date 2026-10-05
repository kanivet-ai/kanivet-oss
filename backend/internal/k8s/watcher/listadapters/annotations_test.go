package listadapters

import (
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const appliedSecret = `{"apiVersion":"v1","data":{"password":"c3VwZXItc2VjcmV0"},"kind":"Secret","metadata":{"name":"db","namespace":"prod"}}`

// kubectl apply copies the whole manifest, Secret data included, into
// last-applied-configuration. List rows are sent to the renderer and
// persisted as snapshots, so the copy must not ride along.
func TestSecretListRowDropsLastAppliedConfiguration(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	ann := map[string]interface{}{
		lastAppliedAnnotation:                   appliedSecret,
		"kapp.k14s.io/original":                 appliedSecret,
		"vcluster.loft.sh/token-abc":            "token",
		"vcluster.loft.sh/object-name":          "db",
		"meta.helm.sh/release-name":             "db",
		"banzaicloud.io/last-applied":           appliedSecret,
		"kapp.k14s.io/original-diff-md5":        "x",
		"kubectl.kubernetes.io/restartedAt":     "2026-01-01T00:00:00Z",
		"checksum/config":                       "abc",
		"deployment.kubernetes.io/revision-not": "1",
	}
	in := u(map[string]interface{}{
		"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]interface{}{"name": "db", "namespace": "prod", "annotations": ann},
		"data":     map[string]interface{}{"password": "c3VwZXItc2VjcmV0"},
	})

	got := Simplify(in, gvr)

	kept, _ := got["annotations"].(map[string]interface{})
	for k, v := range kept {
		if s, _ := v.(string); strings.Contains(s, "c3VwZXItc2VjcmV0") {
			t.Fatalf("list row carries the secret value through annotation %s", k)
		}
	}
	for _, k := range []string{lastAppliedAnnotation, "kapp.k14s.io/original", "kapp.k14s.io/original-diff-md5", "banzaicloud.io/last-applied", "vcluster.loft.sh/token-abc"} {
		if _, ok := kept[k]; ok {
			t.Errorf("annotation %s must be omitted from list rows", k)
		}
	}
	for _, k := range []string{"vcluster.loft.sh/object-name", "meta.helm.sh/release-name", "kubectl.kubernetes.io/restartedAt", "checksum/config", "deployment.kubernetes.io/revision-not"} {
		if _, ok := kept[k]; !ok {
			t.Errorf("annotation %s must be kept", k)
		}
	}
	if len(ann) != 10 {
		t.Fatalf("the object's own annotations must not be modified, %d left", len(ann))
	}
	if got["dataCount"] != 1 || got["type"] != "Opaque" {
		t.Fatalf("secret row lost its fields: %v", got)
	}
}

func TestConfigMapListRowWithOnlyLastAppliedHasNoAnnotations(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	big := `{"data":{"config.yaml":"` + strings.Repeat("x", 64<<10) + `"}}`
	in := u(map[string]interface{}{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]interface{}{"name": "cfg", "namespace": "ns", "annotations": map[string]interface{}{lastAppliedAnnotation: big}},
		"data":     map[string]interface{}{"config.yaml": "x"},
	})

	got := Simplify(in, gvr)

	if ann, ok := got["annotations"].(map[string]interface{}); ok {
		t.Fatalf("a row whose only annotation is omitted must carry none, got %d", len(ann))
	}
}

// Rows without any omitted annotation share the object's map, as before.
func TestListAnnotationsKeepsMapWhenNothingIsOmitted(t *testing.T) {
	ann := map[string]interface{}{"a": "b"}
	got, ok := listAnnotations(ann).(map[string]interface{})
	if !ok || len(got) != 1 || got["a"] != "b" {
		t.Fatalf("annotations changed: %v", got)
	}
}

func TestTypedPodRowDropsLastAppliedConfiguration(t *testing.T) {
	p := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "p", Namespace: "ns",
		Annotations: map[string]string{
			lastAppliedAnnotation:        appliedSecret,
			"vcluster.loft.sh/token-abc": "token",
			"prometheus.io/scrape":       "true",
		},
	}}

	got := typedPod(p, schema.GroupVersionResource{Version: "v1", Resource: "pods"})

	ann, _ := got["annotations"].(map[string]interface{})
	if len(ann) != 1 || ann["prometheus.io/scrape"] != "true" {
		keys := make([]string, 0, len(ann))
		for k := range ann {
			keys = append(keys, k)
		}
		t.Fatalf("typed pod row annotations = %v, want only prometheus.io/scrape", keys)
	}
	if len(p.Annotations) != 3 {
		t.Fatalf("the pod's own annotations must not be modified")
	}
}

// OpenShift (4.15 and earlier) puts each service account's bearer token on
// its dockercfg Secret, and Rancher's wrangler apply keeps a gzipped copy of
// the applied manifest, Secret data included. Neither may reach a list row.
func TestSecretListRowDropsTokenAndAppliedCopies(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	in := u(map[string]interface{}{
		"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/dockercfg",
		"metadata": map[string]interface{}{"name": "builder-dockercfg-x", "namespace": "prod", "annotations": map[string]interface{}{
			"openshift.io/token-secret.name":  "builder-token-x",
			"openshift.io/token-secret.value": "eyJhbGciOiJSUzI1NiJ9.SA-TOKEN",
			"objectset.rio.cattle.io/applied": "H4sIAAAAAAAA/applied-manifest",
			"objectset.rio.cattle.io/id":      "rke2",
		}},
	})

	got := Simplify(in, gvr)

	kept, _ := got["annotations"].(map[string]interface{})
	for _, k := range []string{"openshift.io/token-secret.value", "objectset.rio.cattle.io/applied"} {
		if _, ok := kept[k]; ok {
			t.Errorf("annotation %s must be omitted from list rows", k)
		}
	}
	for _, k := range []string{"openshift.io/token-secret.name", "objectset.rio.cattle.io/id"} {
		if _, ok := kept[k]; !ok {
			t.Errorf("annotation %s must be kept", k)
		}
	}
}
