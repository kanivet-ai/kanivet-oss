package watcher

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type resourceLister interface {
	List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
	Namespace(ns string) resourceLister
}

type dynamicLister struct {
	base dynamic.NamespaceableResourceInterface
	ns   string
}

func newDynamicLister(base dynamic.NamespaceableResourceInterface) resourceLister {
	return &dynamicLister{base: base}
}

func (d *dynamicLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if d.ns == "" {
		return d.base.List(ctx, opts)
	}
	return d.base.Namespace(d.ns).List(ctx, opts)
}

func (d *dynamicLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	if d.ns == "" {
		return d.base.Watch(ctx, opts)
	}
	return d.base.Namespace(d.ns).Watch(ctx, opts)
}

func (d *dynamicLister) Namespace(ns string) resourceLister {
	return &dynamicLister{base: d.base, ns: ns}
}

type typedLister struct {
	client kubernetes.Interface
	gvr    schema.GroupVersionResource
	ns     string
}

func newTypedLister(client kubernetes.Interface, gvr schema.GroupVersionResource) resourceLister {
	return &typedLister{client: client, gvr: gvr}
}

func (t *typedLister) Namespace(ns string) resourceLister {
	return &typedLister{client: t.client, gvr: t.gvr, ns: ns}
}

func (t *typedLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	obj, err := typedList(ctx, t.client, t.gvr, t.ns, opts)
	if err != nil {
		return nil, err
	}
	return typedListToUnstructured(obj)
}

func (t *typedLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	w, err := typedWatch(ctx, t.client, t.gvr, t.ns, opts)
	if err != nil {
		return nil, err
	}
	return newTypedWatchAdapter(w), nil
}

type typedWatchAdapter struct {
	inner  watch.Interface
	out    chan watch.Event
	stopCh chan struct{}
}

func newTypedWatchAdapter(inner watch.Interface) *typedWatchAdapter {
	a := &typedWatchAdapter{
		inner:  inner,
		out:    make(chan watch.Event, 32),
		stopCh: make(chan struct{}),
	}
	go a.run()
	return a
}

func (a *typedWatchAdapter) run() {
	defer close(a.out)
	for {
		select {
		case <-a.stopCh:
			return
		case ev, ok := <-a.inner.ResultChan():
			if !ok {
				return
			}
			if ev.Object == nil {
				select {
				case a.out <- ev:
				case <-a.stopCh:
					return
				}
				continue
			}
			if ev.Type != watch.Error && ev.Type != watch.Bookmark {
				if _, already := ev.Object.(*unstructured.Unstructured); !already {
					if m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ev.Object); err == nil {
						ev.Object = &unstructured.Unstructured{Object: m}
					}
				}
			}
			select {
			case a.out <- ev:
			case <-a.stopCh:
				return
			}
		}
	}
}

func (a *typedWatchAdapter) Stop() {
	select {
	case <-a.stopCh:
	default:
		close(a.stopCh)
	}
	a.inner.Stop()
}

func (a *typedWatchAdapter) ResultChan() <-chan watch.Event { return a.out }

func typedListToUnstructured(obj runtime.Object) (*unstructured.UnstructuredList, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	ul := &unstructured.UnstructuredList{Object: m}
	rawItems, _ := m["items"].([]interface{})
	ul.Items = make([]unstructured.Unstructured, 0, len(rawItems))
	for _, raw := range rawItems {
		if im, ok := raw.(map[string]interface{}); ok {
			ul.Items = append(ul.Items, unstructured.Unstructured{Object: im})
		}
	}
	delete(m, "items")
	return ul, nil
}

func typedList(ctx context.Context, c kubernetes.Interface, gvr schema.GroupVersionResource, ns string, opts metav1.ListOptions) (runtime.Object, error) {
	switch gvr.Group {
	case "":
		switch gvr.Resource {
		case "pods":
			if ns == "" {
				return c.CoreV1().Pods("").List(ctx, opts)
			}
			return c.CoreV1().Pods(ns).List(ctx, opts)
		case "services":
			if ns == "" {
				return c.CoreV1().Services("").List(ctx, opts)
			}
			return c.CoreV1().Services(ns).List(ctx, opts)
		case "configmaps":
			if ns == "" {
				return c.CoreV1().ConfigMaps("").List(ctx, opts)
			}
			return c.CoreV1().ConfigMaps(ns).List(ctx, opts)
		case "secrets":
			if ns == "" {
				return c.CoreV1().Secrets("").List(ctx, opts)
			}
			return c.CoreV1().Secrets(ns).List(ctx, opts)
		case "serviceaccounts":
			if ns == "" {
				return c.CoreV1().ServiceAccounts("").List(ctx, opts)
			}
			return c.CoreV1().ServiceAccounts(ns).List(ctx, opts)
		case "persistentvolumeclaims":
			if ns == "" {
				return c.CoreV1().PersistentVolumeClaims("").List(ctx, opts)
			}
			return c.CoreV1().PersistentVolumeClaims(ns).List(ctx, opts)
		case "endpoints":
			if ns == "" {
				return c.CoreV1().Endpoints("").List(ctx, opts)
			}
			return c.CoreV1().Endpoints(ns).List(ctx, opts)
		case "nodes":
			return c.CoreV1().Nodes().List(ctx, opts)
		case "namespaces":
			return c.CoreV1().Namespaces().List(ctx, opts)
		case "persistentvolumes":
			return c.CoreV1().PersistentVolumes().List(ctx, opts)
		case "replicationcontrollers":
			if ns == "" {
				return c.CoreV1().ReplicationControllers("").List(ctx, opts)
			}
			return c.CoreV1().ReplicationControllers(ns).List(ctx, opts)
		}
	case "apps":
		switch gvr.Resource {
		case "deployments":
			if ns == "" {
				return c.AppsV1().Deployments("").List(ctx, opts)
			}
			return c.AppsV1().Deployments(ns).List(ctx, opts)
		case "statefulsets":
			if ns == "" {
				return c.AppsV1().StatefulSets("").List(ctx, opts)
			}
			return c.AppsV1().StatefulSets(ns).List(ctx, opts)
		case "daemonsets":
			if ns == "" {
				return c.AppsV1().DaemonSets("").List(ctx, opts)
			}
			return c.AppsV1().DaemonSets(ns).List(ctx, opts)
		case "replicasets":
			if ns == "" {
				return c.AppsV1().ReplicaSets("").List(ctx, opts)
			}
			return c.AppsV1().ReplicaSets(ns).List(ctx, opts)
		}
	case "batch":
		switch gvr.Resource {
		case "jobs":
			if ns == "" {
				return c.BatchV1().Jobs("").List(ctx, opts)
			}
			return c.BatchV1().Jobs(ns).List(ctx, opts)
		case "cronjobs":
			if ns == "" {
				return c.BatchV1().CronJobs("").List(ctx, opts)
			}
			return c.BatchV1().CronJobs(ns).List(ctx, opts)
		}
	case "networking.k8s.io":
		switch gvr.Resource {
		case "ingresses":
			if ns == "" {
				return c.NetworkingV1().Ingresses("").List(ctx, opts)
			}
			return c.NetworkingV1().Ingresses(ns).List(ctx, opts)
		case "networkpolicies":
			if ns == "" {
				return c.NetworkingV1().NetworkPolicies("").List(ctx, opts)
			}
			return c.NetworkingV1().NetworkPolicies(ns).List(ctx, opts)
		case "ingressclasses":
			return c.NetworkingV1().IngressClasses().List(ctx, opts)
		}
	case "rbac.authorization.k8s.io":
		switch gvr.Resource {
		case "roles":
			if ns == "" {
				return c.RbacV1().Roles("").List(ctx, opts)
			}
			return c.RbacV1().Roles(ns).List(ctx, opts)
		case "rolebindings":
			if ns == "" {
				return c.RbacV1().RoleBindings("").List(ctx, opts)
			}
			return c.RbacV1().RoleBindings(ns).List(ctx, opts)
		case "clusterroles":
			return c.RbacV1().ClusterRoles().List(ctx, opts)
		case "clusterrolebindings":
			return c.RbacV1().ClusterRoleBindings().List(ctx, opts)
		}
	case "storage.k8s.io":
		switch gvr.Resource {
		case "storageclasses":
			return c.StorageV1().StorageClasses().List(ctx, opts)
		case "csidrivers":
			return c.StorageV1().CSIDrivers().List(ctx, opts)
		case "csinodes":
			return c.StorageV1().CSINodes().List(ctx, opts)
		}
	case "policy":
		if gvr.Resource == "poddisruptionbudgets" {
			if ns == "" {
				return c.PolicyV1().PodDisruptionBudgets("").List(ctx, opts)
			}
			return c.PolicyV1().PodDisruptionBudgets(ns).List(ctx, opts)
		}
	case "autoscaling":
		if gvr.Resource == "horizontalpodautoscalers" {
			if ns == "" {
				return c.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, opts)
			}
			return c.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, opts)
		}
	}
	return nil, fmt.Errorf("no typed client for %s/%s/%s", gvr.Group, gvr.Version, gvr.Resource)
}

func typedWatch(ctx context.Context, c kubernetes.Interface, gvr schema.GroupVersionResource, ns string, opts metav1.ListOptions) (watch.Interface, error) {
	switch gvr.Group {
	case "":
		switch gvr.Resource {
		case "pods":
			if ns == "" {
				return c.CoreV1().Pods("").Watch(ctx, opts)
			}
			return c.CoreV1().Pods(ns).Watch(ctx, opts)
		case "services":
			if ns == "" {
				return c.CoreV1().Services("").Watch(ctx, opts)
			}
			return c.CoreV1().Services(ns).Watch(ctx, opts)
		case "configmaps":
			if ns == "" {
				return c.CoreV1().ConfigMaps("").Watch(ctx, opts)
			}
			return c.CoreV1().ConfigMaps(ns).Watch(ctx, opts)
		case "secrets":
			if ns == "" {
				return c.CoreV1().Secrets("").Watch(ctx, opts)
			}
			return c.CoreV1().Secrets(ns).Watch(ctx, opts)
		case "serviceaccounts":
			if ns == "" {
				return c.CoreV1().ServiceAccounts("").Watch(ctx, opts)
			}
			return c.CoreV1().ServiceAccounts(ns).Watch(ctx, opts)
		case "persistentvolumeclaims":
			if ns == "" {
				return c.CoreV1().PersistentVolumeClaims("").Watch(ctx, opts)
			}
			return c.CoreV1().PersistentVolumeClaims(ns).Watch(ctx, opts)
		case "endpoints":
			if ns == "" {
				return c.CoreV1().Endpoints("").Watch(ctx, opts)
			}
			return c.CoreV1().Endpoints(ns).Watch(ctx, opts)
		case "nodes":
			return c.CoreV1().Nodes().Watch(ctx, opts)
		case "namespaces":
			return c.CoreV1().Namespaces().Watch(ctx, opts)
		case "persistentvolumes":
			return c.CoreV1().PersistentVolumes().Watch(ctx, opts)
		case "replicationcontrollers":
			if ns == "" {
				return c.CoreV1().ReplicationControllers("").Watch(ctx, opts)
			}
			return c.CoreV1().ReplicationControllers(ns).Watch(ctx, opts)
		}
	case "apps":
		switch gvr.Resource {
		case "deployments":
			if ns == "" {
				return c.AppsV1().Deployments("").Watch(ctx, opts)
			}
			return c.AppsV1().Deployments(ns).Watch(ctx, opts)
		case "statefulsets":
			if ns == "" {
				return c.AppsV1().StatefulSets("").Watch(ctx, opts)
			}
			return c.AppsV1().StatefulSets(ns).Watch(ctx, opts)
		case "daemonsets":
			if ns == "" {
				return c.AppsV1().DaemonSets("").Watch(ctx, opts)
			}
			return c.AppsV1().DaemonSets(ns).Watch(ctx, opts)
		case "replicasets":
			if ns == "" {
				return c.AppsV1().ReplicaSets("").Watch(ctx, opts)
			}
			return c.AppsV1().ReplicaSets(ns).Watch(ctx, opts)
		}
	case "batch":
		switch gvr.Resource {
		case "jobs":
			if ns == "" {
				return c.BatchV1().Jobs("").Watch(ctx, opts)
			}
			return c.BatchV1().Jobs(ns).Watch(ctx, opts)
		case "cronjobs":
			if ns == "" {
				return c.BatchV1().CronJobs("").Watch(ctx, opts)
			}
			return c.BatchV1().CronJobs(ns).Watch(ctx, opts)
		}
	case "networking.k8s.io":
		switch gvr.Resource {
		case "ingresses":
			if ns == "" {
				return c.NetworkingV1().Ingresses("").Watch(ctx, opts)
			}
			return c.NetworkingV1().Ingresses(ns).Watch(ctx, opts)
		case "networkpolicies":
			if ns == "" {
				return c.NetworkingV1().NetworkPolicies("").Watch(ctx, opts)
			}
			return c.NetworkingV1().NetworkPolicies(ns).Watch(ctx, opts)
		case "ingressclasses":
			return c.NetworkingV1().IngressClasses().Watch(ctx, opts)
		}
	case "rbac.authorization.k8s.io":
		switch gvr.Resource {
		case "roles":
			if ns == "" {
				return c.RbacV1().Roles("").Watch(ctx, opts)
			}
			return c.RbacV1().Roles(ns).Watch(ctx, opts)
		case "rolebindings":
			if ns == "" {
				return c.RbacV1().RoleBindings("").Watch(ctx, opts)
			}
			return c.RbacV1().RoleBindings(ns).Watch(ctx, opts)
		case "clusterroles":
			return c.RbacV1().ClusterRoles().Watch(ctx, opts)
		case "clusterrolebindings":
			return c.RbacV1().ClusterRoleBindings().Watch(ctx, opts)
		}
	case "storage.k8s.io":
		switch gvr.Resource {
		case "storageclasses":
			return c.StorageV1().StorageClasses().Watch(ctx, opts)
		case "csidrivers":
			return c.StorageV1().CSIDrivers().Watch(ctx, opts)
		case "csinodes":
			return c.StorageV1().CSINodes().Watch(ctx, opts)
		}
	case "policy":
		if gvr.Resource == "poddisruptionbudgets" {
			if ns == "" {
				return c.PolicyV1().PodDisruptionBudgets("").Watch(ctx, opts)
			}
			return c.PolicyV1().PodDisruptionBudgets(ns).Watch(ctx, opts)
		}
	case "autoscaling":
		if gvr.Resource == "horizontalpodautoscalers" {
			if ns == "" {
				return c.AutoscalingV2().HorizontalPodAutoscalers("").Watch(ctx, opts)
			}
			return c.AutoscalingV2().HorizontalPodAutoscalers(ns).Watch(ctx, opts)
		}
	}
	return nil, fmt.Errorf("no typed client for %s/%s/%s", gvr.Group, gvr.Version, gvr.Resource)
}

var nativeKinds = map[string]map[string]bool{
	"": {
		"pods": true, "services": true, "configmaps": true, "secrets": true,
		"serviceaccounts": true, "persistentvolumeclaims": true, "endpoints": true,
		"nodes": true, "namespaces": true, "persistentvolumes": true, "replicationcontrollers": true,
	},
	"apps":                     {"deployments": true, "statefulsets": true, "daemonsets": true, "replicasets": true},
	"batch":                    {"jobs": true, "cronjobs": true},
	"networking.k8s.io":        {"ingresses": true, "networkpolicies": true, "ingressclasses": true},
	"rbac.authorization.k8s.io": {"roles": true, "rolebindings": true, "clusterroles": true, "clusterrolebindings": true},
	"storage.k8s.io":           {"storageclasses": true, "csidrivers": true, "csinodes": true},
	"policy":                   {"poddisruptionbudgets": true},
	"autoscaling":              {"horizontalpodautoscalers": true},
}

func isNativeKind(gvr schema.GroupVersionResource) bool {
	if m, ok := nativeKinds[gvr.Group]; ok {
		return m[gvr.Resource]
	}
	return false
}
