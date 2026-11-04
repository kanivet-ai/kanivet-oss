package mcp

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// kindInfo is a resolved API resource.
type kindInfo struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
}

func (k kindInfo) APIVersion() string {
	if k.GVR.Group == "" {
		return k.GVR.Version
	}
	return k.GVR.Group + "/" + k.GVR.Version
}

// Well-known kinds resolve without discovery.
var builtinKinds = map[string]kindInfo{
	"pod":                      {schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "Pod", true},
	"service":                  {schema.GroupVersionResource{Version: "v1", Resource: "services"}, "Service", true},
	"configmap":                {schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, "ConfigMap", true},
	"secret":                   {schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, "Secret", true},
	"node":                     {schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, "Node", false},
	"namespace":                {schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "Namespace", false},
	"event":                    {schema.GroupVersionResource{Version: "v1", Resource: "events"}, "Event", true},
	"persistentvolumeclaim":    {schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, "PersistentVolumeClaim", true},
	"persistentvolume":         {schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}, "PersistentVolume", false},
	"serviceaccount":           {schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, "ServiceAccount", true},
	"endpoints":                {schema.GroupVersionResource{Version: "v1", Resource: "endpoints"}, "Endpoints", true},
	"deployment":               {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "Deployment", true},
	"statefulset":              {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, "StatefulSet", true},
	"daemonset":                {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, "DaemonSet", true},
	"replicaset":               {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, "ReplicaSet", true},
	"job":                      {schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, "Job", true},
	"cronjob":                  {schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, "CronJob", true},
	"ingress":                  {schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, "Ingress", true},
	"networkpolicy":            {schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, "NetworkPolicy", true},
	"horizontalpodautoscaler":  {schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}, "HorizontalPodAutoscaler", true},
	"poddisruptionbudget":      {schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}, "PodDisruptionBudget", true},
	"storageclass":             {schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}, "StorageClass", false},
	"role":                     {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, "Role", true},
	"rolebinding":              {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, "RoleBinding", true},
	"clusterrole":              {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, "ClusterRole", false},
	"clusterrolebinding":       {schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}, "ClusterRoleBinding", false},
	"customresourcedefinition": {schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}, "CustomResourceDefinition", false},
}

var kindAliases = map[string]string{
	"po": "pod", "pods": "pod", "svc": "service", "services": "service", "cm": "configmap", "configmaps": "configmap",
	"secrets": "secret", "no": "node", "nodes": "node", "ns": "namespace", "namespaces": "namespace", "ev": "event", "events": "event",
	"pvc": "persistentvolumeclaim", "persistentvolumeclaims": "persistentvolumeclaim", "pv": "persistentvolume", "persistentvolumes": "persistentvolume",
	"sa": "serviceaccount", "serviceaccounts": "serviceaccount", "ep": "endpoints",
	"deploy": "deployment", "deployments": "deployment", "sts": "statefulset", "statefulsets": "statefulset",
	"ds": "daemonset", "daemonsets": "daemonset", "rs": "replicaset", "replicasets": "replicaset",
	"jobs": "job", "cj": "cronjob", "cronjobs": "cronjob", "ing": "ingress", "ingresses": "ingress",
	"netpol": "networkpolicy", "networkpolicies": "networkpolicy", "hpa": "horizontalpodautoscaler", "horizontalpodautoscalers": "horizontalpodautoscaler",
	"pdb": "poddisruptionbudget", "poddisruptionbudgets": "poddisruptionbudget", "sc": "storageclass", "storageclasses": "storageclass",
	"roles": "role", "rolebindings": "rolebinding", "clusterroles": "clusterrole", "clusterrolebindings": "clusterrolebinding",
	"crd": "customresourcedefinition", "crds": "customresourcedefinition", "customresourcedefinitions": "customresourcedefinition",
}

type discoveryCache struct {
	mu      sync.Mutex
	entries map[string]discoveryEntry
}

type discoveryEntry struct {
	at        time.Time
	resources []metav1.APIResource
}

var discovered = &discoveryCache{entries: map[string]discoveryEntry{}}

// resolveKind maps "Pod", "pods", "deploy", "certificates.cert-manager.io"
// or "Certificate" to an API resource.
func (d *Deps) resolveKind(cluster, kind string) (kindInfo, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return kindInfo{}, fmt.Errorf("kind is required")
	}
	if a, ok := kindAliases[k]; ok {
		k = a
	}
	if ki, ok := builtinKinds[k]; ok {
		return ki, nil
	}
	if d.K8s == nil {
		return kindInfo{}, fmt.Errorf("unknown kind %q", kind)
	}
	resources, err := d.apiResources(cluster)
	if err != nil {
		return kindInfo{}, err
	}
	name, group := k, ""
	if i := strings.Index(k, "."); i > 0 {
		name, group = k[:i], k[i+1:]
	}
	var matches []metav1.APIResource
	for _, r := range resources {
		if group != "" && r.Group != group {
			continue
		}
		if strings.EqualFold(r.Kind, name) || r.Name == name || r.SingularName == name || contains(r.ShortNames, name) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return kindInfo{}, fmt.Errorf("unknown kind %q on this cluster", kind)
	}
	// Prefer the group's preferred version, then a stable version.
	sort.SliceStable(matches, func(i, j int) bool {
		pi, pj := d.preferred(cluster, matches[i]), d.preferred(cluster, matches[j])
		if pi != pj {
			return pi
		}
		return !strings.Contains(matches[i].Version, "alpha") && !strings.Contains(matches[i].Version, "beta")
	})
	m := matches[0]
	return kindInfo{GVR: schema.GroupVersionResource{Group: m.Group, Version: m.Version, Resource: m.Name}, Kind: m.Kind, Namespaced: m.Namespaced}, nil
}

func (d *Deps) preferred(cluster string, r metav1.APIResource) bool {
	v, ok := d.K8s.PreferredVersion(cluster, r.Group)
	return ok && v == r.Version
}

func (d *Deps) apiResources(cluster string) ([]metav1.APIResource, error) {
	discovered.mu.Lock()
	e, ok := discovered.entries[cluster]
	discovered.mu.Unlock()
	if ok && time.Since(e.at) < 5*time.Minute {
		return e.resources, nil
	}
	res, err := d.K8s.ListAPIResources(cluster)
	if err != nil {
		return nil, err
	}
	discovered.mu.Lock()
	discovered.entries[cluster] = discoveryEntry{at: time.Now(), resources: res}
	discovered.mu.Unlock()
	return res, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
