package k8s

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var configMapGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

func configMap(name, namespace, value string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"data":       map[string]interface{}{"key": value},
	}}
}

// Editing an object that was deleted while the editor was open must fail,
// not quietly bring the object back.
func TestUpdateResourceDoesNotRecreateDeletedObject(t *testing.T) {
	c := newTestClient()
	fake := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.dynamic["c1"] = fake

	_, err := c.UpdateResource(context.Background(), "c1", configMapGVR, "ns", "cfg", configMap("cfg", "ns", "stale"))
	if err == nil {
		t.Fatal("expected an error updating a missing object, got nil")
	}
	if !errors.Is(err, ErrResourceGone) {
		t.Fatalf("error should wrap ErrResourceGone, got: %v", err)
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("error should still carry the apiserver NotFound status, got: %v", err)
	}

	_, getErr := fake.Resource(configMapGVR).Namespace("ns").Get(context.Background(), "cfg", metav1.GetOptions{})
	if !apierrors.IsNotFound(getErr) {
		t.Fatalf("object must not exist after a failed update, Get returned: %v", getErr)
	}
}

// The YAML editor never sees kubectl's last-applied-configuration (the
// detail endpoint hides it), so saving must not delete it from the object.
func TestUpdateResourceKeepsLastAppliedConfiguration(t *testing.T) {
	c := newTestClient()
	live := configMap("cfg", "ns", "old")
	live.SetAnnotations(map[string]string{lastAppliedAnnotation: `{"data":{"key":"old"}}`, "team": "core"})
	fake := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live)
	c.dynamic["c1"] = fake
	edited := configMap("cfg", "ns", "new")
	edited.SetAnnotations(map[string]string{"team": "core"})

	got, err := c.UpdateResource(context.Background(), "c1", configMapGVR, "ns", "cfg", edited)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	if applied := got.GetAnnotations()[lastAppliedAnnotation]; applied != `{"data":{"key":"old"}}` {
		t.Fatalf("last-applied-configuration = %q after a save from the editor", applied)
	}
	if value, _, _ := unstructured.NestedString(got.Object, "data", "key"); value != "new" {
		t.Fatalf("update did not apply, data.key = %q", value)
	}
}

// A manifest that carries its own last-applied-configuration is saved as is.
func TestUpdateResourceSavesAGivenLastAppliedConfiguration(t *testing.T) {
	c := newTestClient()
	live := configMap("cfg", "ns", "old")
	live.SetAnnotations(map[string]string{lastAppliedAnnotation: "old"})
	c.dynamic["c1"] = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live)
	edited := configMap("cfg", "ns", "new")
	edited.SetAnnotations(map[string]string{lastAppliedAnnotation: "new"})

	got, err := c.UpdateResource(context.Background(), "c1", configMapGVR, "ns", "cfg", edited)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if applied := got.GetAnnotations()[lastAppliedAnnotation]; applied != "new" {
		t.Fatalf("last-applied-configuration = %q, want the saved one", applied)
	}
}

func TestUpdateResourceUpdatesExistingObject(t *testing.T) {
	c := newTestClient()
	fake := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), configMap("cfg", "ns", "old"))
	c.dynamic["c1"] = fake

	got, err := c.UpdateResource(context.Background(), "c1", configMapGVR, "ns", "cfg", configMap("cfg", "ns", "new"))
	if err != nil {
		t.Fatalf("update of an existing object failed: %v", err)
	}
	value, _, _ := unstructured.NestedString(got.Object, "data", "key")
	if value != "new" {
		t.Fatalf("update did not apply, data.key = %q", value)
	}
}
