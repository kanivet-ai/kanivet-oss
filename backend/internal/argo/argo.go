package argo

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
)

const (
	GroupArgoproj      = "argoproj.io"
	ApplicationKind    = "Application"
	ApplicationSetKind = "ApplicationSet"
	AppProjectKind     = "AppProject"
)

var trackedKinds = []struct {
	Kind     string
	Plural   string
	Singular string
}{
	{ApplicationKind, "applications", "application"},
	{ApplicationSetKind, "applicationsets", "applicationset"},
	{AppProjectKind, "appprojects", "appproject"},
}

type Resource struct {
	Name       string `json:"name"`
	Group      string `json:"group"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Namespaced bool   `json:"namespaced"`
}

type Detection struct {
	Installed  bool   `json:"installed"`
	Namespace  string `json:"namespace,omitempty"`
	HasAPI     bool   `json:"hasApi"`
	ServerName string `json:"serverName,omitempty"`
}

func IsArgoGroup(group string) bool {
	return group == GroupArgoproj || strings.HasSuffix(group, "."+GroupArgoproj)
}

func Detect(ctx context.Context, kube kubernetes.Interface, meta metadata.Interface, dyn dynamic.Interface) (*Detection, error) {
	det := &Detection{}
	if meta == nil && dyn == nil {
		return det, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Detect via a metadata GET of the Application CRD by name: a single tiny
	// PartialObjectMetadata request instead of listing every CRD body. The CRD
	// path is stable regardless of discovery-cache warmth (metadata/dynamic
	// clients build URLs directly from the GVR, no REST mapper involved).
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	probed := false
	if meta != nil {
		if _, err := meta.Resource(crdGVR).Get(timeoutCtx, "applications.argoproj.io", metav1.GetOptions{}); err == nil {
			det.Installed = true
			probed = true
		} else if apierrors.IsNotFound(err) {
			probed = true
		}
	}
	if !probed && dyn != nil {
		// CRD GET failed for a non-NotFound reason (RBAC, transient apiserver
		// issue, etc.). Fall back to a direct probe of the Application GVR;
		// treat any error as "unknown / not installed" so detection stays
		// best-effort.
		gvr := schema.GroupVersionResource{Group: GroupArgoproj, Version: "v1alpha1", Resource: "applications"}
		if _, err := dyn.Resource(gvr).List(timeoutCtx, metav1.ListOptions{Limit: 1}); err == nil {
			det.Installed = true
		}
	}

	if !det.Installed {
		return det, nil
	}

	if kube == nil {
		return det, nil
	}
	candidates := []string{"argocd", "argo-cd", "argo"}
	for _, ns := range candidates {
		svc, err := kube.CoreV1().Services(ns).Get(timeoutCtx, "argocd-server", metav1.GetOptions{})
		if err == nil && svc != nil {
			det.HasAPI = true
			det.Namespace = ns
			det.ServerName = svc.Name
			return det, nil
		}
	}
	svcList, err := kube.CoreV1().Services(metav1.NamespaceAll).List(timeoutCtx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=argocd-server"})
	if err == nil && svcList != nil && len(svcList.Items) > 0 {
		det.HasAPI = true
		det.Namespace = svcList.Items[0].Namespace
		det.ServerName = svcList.Items[0].Name
	}
	return det, nil
}

func ListResources(ctx context.Context, dyn dynamic.Interface) ([]Resource, error) {
	if dyn == nil {
		return nil, fmt.Errorf("nil dynamic client")
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out := make([]Resource, 0, len(trackedKinds))
	for _, k := range trackedKinds {
		gvr := schema.GroupVersionResource{Group: GroupArgoproj, Version: "v1alpha1", Resource: k.Plural}
		if _, err := dyn.Resource(gvr).List(timeoutCtx, metav1.ListOptions{Limit: 1}); err != nil {
			if isNotFound(err) {
				continue
			}
		}
		out = append(out, Resource{
			Name:       k.Plural,
			Group:      GroupArgoproj,
			Version:    "v1alpha1",
			Kind:       k.Kind,
			Namespaced: true,
		})
	}
	return out, nil
}

func GetApplication(ctx context.Context, dyn dynamic.Interface, namespace, name string) (*unstructured.Unstructured, error) {
	gvr := schema.GroupVersionResource{Group: GroupArgoproj, Version: "v1alpha1", Resource: "applications"}
	return dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "could not find the requested resource") || strings.Contains(msg, "no matches for kind") || strings.Contains(msg, "not found")
}
