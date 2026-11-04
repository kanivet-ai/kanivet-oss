// Package crdprobe answers "is this CRD installed, and which version does the
// API server serve?" with one GET per CRD. Integrations (Flux, CloudNativePG)
// use it to decide whether to show their sidebar entries and which GVR to read.
package crdprobe

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// CRDGVR is the GVR of CustomResourceDefinition objects.
var CRDGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// Result is the outcome of probing one CRD.
type Result struct {
	Found bool
	// Version is the served version to read: the storage version when it is
	// served, otherwise the first served version.
	Version string
	// Forbidden is true when RBAC hid the CRD from us; Found is then unknown.
	Forbidden bool
}

// Probe GETs the CRD named "<plural>.<group>".
func Probe(ctx context.Context, dyn dynamic.Interface, plural, group string) (Result, error) {
	if dyn == nil {
		return Result{}, nil
	}
	obj, err := dyn.Resource(CRDGVR).Get(ctx, plural+"."+group, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return Result{}, nil
		}
		if apierrors.IsForbidden(err) {
			return Result{Forbidden: true}, nil
		}
		return Result{}, err
	}
	v := ServedVersion(obj)
	return Result{Found: v != "", Version: v}, nil
}

// ServedVersion picks the version to read from a CRD object.
func ServedVersion(crd *unstructured.Unstructured) string {
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	first := ""
	for _, raw := range versions {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		served, _ := m["served"].(bool)
		storage, _ := m["storage"].(bool)
		if !served || name == "" {
			continue
		}
		if storage {
			return name
		}
		if first == "" {
			first = name
		}
	}
	return first
}
