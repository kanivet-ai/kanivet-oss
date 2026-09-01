package listadapters

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func u(obj map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: obj}
}

func TestSimplifyPod(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	in := u(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name":              "p1",
			"namespace":         "ns",
			"uid":               "u1",
			"creationTimestamp": "2026-01-01T00:00:00Z",
			"resourceVersion":   "42",
			"labels":            map[string]interface{}{"app": "x"},
			"annotations":       map[string]interface{}{"a": "b"},
			"ownerReferences":   []interface{}{map[string]interface{}{"kind": "ReplicaSet"}},
		},
		"spec": map[string]interface{}{
			"nodeName": "n1",
			"containers": []interface{}{
				map[string]interface{}{"name": "c1", "image": "nginx", "env": []interface{}{"LARGE"}},
			},
			"volumes": []interface{}{map[string]interface{}{"name": "v1"}},
		},
		"status": map[string]interface{}{
			"phase": "Running",
			"containerStatuses": []interface{}{
				map[string]interface{}{"name": "c1", "ready": true, "state": map[string]interface{}{"running": map[string]interface{}{}}, "restartCount": int64(3), "image": "nginx", "imageID": "sha:1"},
			},
		},
	})

	got := Simplify(in, gvr)

	if got["name"] != "p1" || got["namespace"] != "ns" {
		t.Fatalf("bad meta: %+v", got)
	}
	if got["phase"] != "Running" {
		t.Fatalf("missing phase")
	}
	if got["restarts"] != 3 {
		t.Fatalf("restarts=%v", got["restarts"])
	}
	cs, ok := got["containerStatuses"].([]map[string]interface{})
	if !ok || len(cs) != 1 {
		t.Fatalf("containerStatuses=%T %v", got["containerStatuses"], got["containerStatuses"])
	}
	if _, present := cs[0]["image"]; present {
		t.Fatalf("image must be stripped from containerStatuses: %v", cs[0])
	}
	containers, ok := got["containers"].([]map[string]interface{})
	if !ok || len(containers) != 1 || containers[0]["name"] != "c1" {
		t.Fatalf("containers=%v", got["containers"])
	}
	if _, present := containers[0]["image"]; present {
		t.Fatalf("image must be stripped from containers: %v", containers[0])
	}
	if _, present := got["volumes"]; present {
		t.Fatalf("volumes must be omitted from pod list item")
	}
	if got["nodeName"] != "n1" {
		t.Fatalf("nodeName missing")
	}
	if _, present := got["labels"]; !present {
		t.Fatalf("labels must be preserved")
	}
	if _, present := got["ownerReferences"]; !present {
		t.Fatalf("ownerReferences must be preserved")
	}
}

func TestSimplifyDeployment(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	in := u(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      "d",
			"namespace": "ns",
			"annotations": map[string]interface{}{
				"deployment.kubernetes.io/revision": "7",
				"helm.sh/chart":                     "big-value-that-should-be-stripped",
			},
		},
		"spec": map[string]interface{}{
			"replicas": int64(3),
			"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "x"}},
			"template": map[string]interface{}{"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "x"}}}},
		},
		"status": map[string]interface{}{
			"replicas":          int64(3),
			"readyReplicas":     int64(2),
			"availableReplicas": int64(2),
			"updatedReplicas":   int64(3),
		},
	})
	got := Simplify(in, gvr)
	if got["replicas"].(int64) != 3 {
		t.Fatalf("replicas")
	}
	if got["readyReplicas"].(int64) != 2 {
		t.Fatalf("readyReplicas")
	}
	if got["statusReplicas"].(int64) != 3 {
		t.Fatalf("statusReplicas")
	}
	ann, ok := got["annotations"].(map[string]interface{})
	if !ok {
		t.Fatalf("annotations missing: %T", got["annotations"])
	}
	if ann["deployment.kubernetes.io/revision"] != "7" {
		t.Fatalf("revision missing")
	}
	if _, present := ann["helm.sh/chart"]; present {
		t.Fatalf("non-revision annotations must be stripped: %v", ann)
	}
	if _, present := got["template"]; present {
		t.Fatalf("template must not leak into list item")
	}
}

func TestSimplifyDeploymentWithoutRevisionStripsAnnotations(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	in := u(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      "d-no-rev",
			"namespace": "ns",
			"annotations": map[string]interface{}{
				"helm.sh/chart": "should-not-leak",
			},
		},
		"spec": map[string]interface{}{"replicas": int64(1)},
	})
	got := Simplify(in, gvr)
	if _, present := got["annotations"]; present {
		t.Fatalf("annotations should be omitted when no revision is present: %v", got["annotations"])
	}
}

func TestSimplifyDaemonSet(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	in := u(map[string]interface{}{
		"kind": "DaemonSet",
		"metadata": map[string]interface{}{"name": "ds", "namespace": "kube-system"},
		"status": map[string]interface{}{
			"desiredNumberScheduled": int64(5),
			"numberReady":            int64(4),
			"numberAvailable":        int64(4),
			"updatedNumberScheduled": int64(5),
		},
	})
	got := Simplify(in, gvr)
	if got["desiredNumberScheduled"].(int64) != 5 {
		t.Fatalf("desiredNumberScheduled")
	}
	if got["numberReady"].(int64) != 4 {
		t.Fatalf("numberReady")
	}
}

func TestSimplifyService(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	in := u(map[string]interface{}{
		"kind":     "Service",
		"metadata": map[string]interface{}{"name": "svc", "namespace": "ns"},
		"spec": map[string]interface{}{
			"clusterIP": "10.0.0.1",
			"type":      "ClusterIP",
			"ports":     []interface{}{map[string]interface{}{"port": int64(80)}},
			"selector":  map[string]interface{}{"app": "x"},
		},
		"status": map[string]interface{}{
			"loadBalancer": map[string]interface{}{"ingress": []interface{}{}},
		},
	})
	got := Simplify(in, gvr)
	if got["clusterIP"] != "10.0.0.1" || got["type"] != "ClusterIP" {
		t.Fatalf("bad service: %+v", got)
	}
	if got["ports"] == nil {
		t.Fatalf("ports missing")
	}
}

func TestSimplifyEvent(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}
	in := u(map[string]interface{}{
		"kind":           "Event",
		"metadata":       map[string]interface{}{"name": "e", "namespace": "ns"},
		"type":           "Warning",
		"reason":         "Failed",
		"message":        "boom",
		"count":          int64(3),
		"lastTimestamp":  "2026-01-01T00:00:00Z",
		"involvedObject": map[string]interface{}{"kind": "Pod", "name": "p"},
		"source":         map[string]interface{}{"component": "kubelet"},
	})
	got := Simplify(in, gvr)
	for _, k := range []string{"type", "reason", "message", "count", "lastTimestamp", "involvedObject", "source"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("event missing %s: %+v", k, got)
		}
	}
}

func TestSimplifyConfigMap(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	in := u(map[string]interface{}{
		"kind":     "ConfigMap",
		"metadata": map[string]interface{}{"name": "cm", "namespace": "ns"},
		"data":     map[string]interface{}{"a": "1", "b": "2"},
		"binaryData": map[string]interface{}{"bin": "xxx"},
	})
	got := Simplify(in, gvr)
	if got["dataCount"].(int) != 2 {
		t.Fatalf("dataCount=%v", got["dataCount"])
	}
	if got["binaryDataCount"].(int) != 1 {
		t.Fatalf("binaryDataCount=%v", got["binaryDataCount"])
	}
	if _, present := got["data"]; present {
		t.Fatalf("data must not be included in list item: %v", got)
	}
}

func TestSimplifySecret(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	in := u(map[string]interface{}{
		"kind":     "Secret",
		"metadata": map[string]interface{}{"name": "s", "namespace": "ns"},
		"type":     "kubernetes.io/tls",
		"data":     map[string]interface{}{"tls.crt": "xxx"},
	})
	got := Simplify(in, gvr)
	if got["type"] != "kubernetes.io/tls" {
		t.Fatalf("type=%v", got["type"])
	}
	if got["dataCount"].(int) != 1 {
		t.Fatalf("dataCount")
	}
}

func TestSimplifyEndpointSlice(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}
	in := u(map[string]interface{}{
		"kind":        "EndpointSlice",
		"metadata":    map[string]interface{}{"name": "e", "namespace": "ns"},
		"addressType": "IPv4",
		"endpoints":   []interface{}{map[string]interface{}{"addresses": []interface{}{"10.0.0.1"}}},
		"ports":       []interface{}{map[string]interface{}{"port": int64(80)}},
	})
	got := Simplify(in, gvr)
	if got["addressType"] != "IPv4" {
		t.Fatalf("addressType=%v", got["addressType"])
	}
	if got["endpoints"] == nil || got["ports"] == nil {
		t.Fatalf("endpoints/ports missing")
	}
}

func TestSimplifyNode(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	in := u(map[string]interface{}{
		"kind":     "Node",
		"metadata": map[string]interface{}{"name": "n1", "labels": map[string]interface{}{"node-role.kubernetes.io/master": ""}},
		"spec":     map[string]interface{}{"taints": []interface{}{map[string]interface{}{"key": "x"}}, "unschedulable": true},
		"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}},
			"nodeInfo":   map[string]interface{}{"kubeletVersion": "v1.30.0"},
			"addresses":  []interface{}{map[string]interface{}{"type": "InternalIP", "address": "10.0.0.1"}},
		},
	})
	got := Simplify(in, gvr)
	if got["taints"] == nil || got["conditions"] == nil || got["nodeInfo"] == nil {
		t.Fatalf("node: %+v", got)
	}
}

func TestSimplifyGeneric(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "foos"}
	in := u(map[string]interface{}{
		"apiVersion": "example.com/v1",
		"kind":       "Foo",
		"metadata":   map[string]interface{}{"name": "f", "namespace": "ns"},
		"status": map[string]interface{}{
			"phase":      "Healthy",
			"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}},
		},
		"spec": map[string]interface{}{"bigData": "..."},
	})
	got := Simplify(in, gvr)
	if got["name"] != "f" || got["namespace"] != "ns" || got["kind"] != "Foo" {
		t.Fatalf("generic base: %+v", got)
	}
	if got["phase"] != "Healthy" {
		t.Fatalf("generic phase")
	}
	if _, present := got["bigData"]; present {
		t.Fatalf("generic must not leak spec: %+v", got)
	}
}

func TestSimplifyRoleBinding(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}
	in := u(map[string]interface{}{
		"kind":     "RoleBinding",
		"metadata": map[string]interface{}{"name": "rb", "namespace": "ns"},
		"roleRef":  map[string]interface{}{"kind": "Role", "name": "r"},
		"subjects": []interface{}{map[string]interface{}{"kind": "User", "name": "alice"}},
	})
	got := Simplify(in, gvr)
	if got["roleRef"] == nil || got["subjects"] == nil {
		t.Fatalf("rolebinding: %+v", got)
	}
}

func TestSimplifyPreservesKind(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	in := u(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": "d", "namespace": "ns"},
	})
	got := Simplify(in, gvr)
	if got["kind"] != "Deployment" || got["apiVersion"] != "apps/v1" {
		t.Fatalf("kind/apiVersion: %+v", got)
	}
}
