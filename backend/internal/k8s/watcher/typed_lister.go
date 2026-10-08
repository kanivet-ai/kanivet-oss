package watcher

import (
	"context"
	"errors"
	"fmt"
	"log"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/kanivet/backend/internal/k8s/watcher/listadapters"
)

type resourceLister interface {
	List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
	Namespace(ns string) resourceLister
}

// itemLister is a resourceLister that can hand a list over one item at a
// time, as the response arrives, instead of whole once it is complete. Items
// come in the form watch events carry them: typed where a typed adapter reads
// them, unstructured otherwise.
type itemLister interface {
	ListEach(ctx context.Context, opts metav1.ListOptions, each func(runtime.Object) error) (resourceVersion string, err error)
}

type dynamicLister struct {
	base dynamic.NamespaceableResourceInterface
	ns   string
	// rc reads the list as a stream. Without one it is decoded whole.
	rc  rest.Interface
	gvr schema.GroupVersionResource
}

func newDynamicLister(base dynamic.NamespaceableResourceInterface) resourceLister {
	return &dynamicLister{base: base}
}

// newStreamingDynamicLister is newDynamicLister for a resource whose list rc
// can also fetch as a stream.
func newStreamingDynamicLister(base dynamic.NamespaceableResourceInterface, rc rest.Interface, gvr schema.GroupVersionResource) resourceLister {
	return &dynamicLister{base: base, rc: usableREST(rc), gvr: gvr}
}

// usableREST returns rc unless it is the typed nil a fake clientset hands out.
func usableREST(rc rest.Interface) rest.Interface {
	if c, ok := rc.(*rest.RESTClient); ok && c == nil {
		return nil
	}
	return rc
}

func (d *dynamicLister) ListEach(ctx context.Context, opts metav1.ListOptions, each func(runtime.Object) error) (string, error) {
	if d.rc != nil {
		rv, err := d.stream(ctx, opts, each)
		if !errors.Is(err, errListFormat) {
			return rv, err
		}
		log.Printf("k8s watcher: %s list cannot be read as a stream (%v), decoding it whole", d.gvr.Resource, err)
	}
	list, err := d.List(ctx, opts)
	if err != nil {
		return "", err
	}
	for i := range list.Items {
		if err := each(&list.Items[i]); err != nil {
			return list.GetResourceVersion(), err
		}
	}
	return list.GetResourceVersion(), nil
}

func (d *dynamicLister) stream(ctx context.Context, opts metav1.ListOptions, each func(runtime.Object) error) (string, error) {
	path := []string{"apis", d.gvr.Group, d.gvr.Version}
	if d.gvr.Group == "" {
		path = []string{"api", d.gvr.Version}
	}
	if d.ns != "" {
		path = append(path, "namespaces", d.ns)
	}
	path = append(path, d.gvr.Resource)
	// Custom resources are only served as JSON; asking for it also keeps a
	// built-in kind from answering in protobuf.
	body, err := d.rc.Get().AbsPath(path...).
		VersionedParams(&opts, scheme.ParameterCodec).
		SetHeader("Accept", "application/json").
		Stream(ctx)
	if err != nil {
		return "", err
	}
	defer body.Close()
	return decodeJSONList(body, each)
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
	return &dynamicLister{base: d.base, ns: ns, rc: d.rc, gvr: d.gvr}
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
	if ul, ok := listadapters.TypedList(obj, t.gvr); ok {
		return ul, nil
	}
	_ = meta.EachListItem(obj, func(o runtime.Object) error {
		slimTyped(o)
		return nil
	})
	return typedListToUnstructured(obj)
}

// ListEach reads the list as a protobuf stream. A client that cannot stream (a
// fake one) or a response that is not the expected protobuf gets the ordinary
// typed list instead, handed over item by item all the same.
func (t *typedLister) ListEach(ctx context.Context, opts metav1.ListOptions, each func(runtime.Object) error) (string, error) {
	emit := func(obj runtime.Object) error { return each(asListed(obj)) }
	if rv, err := t.stream(ctx, opts, emit); !errors.Is(err, errListFormat) {
		return rv, err
	}
	obj, err := typedList(ctx, t.client, t.gvr, t.ns, opts)
	if err != nil {
		return "", err
	}
	var rv string
	if lm, err := meta.ListAccessor(obj); err == nil {
		rv = lm.GetResourceVersion()
	}
	return rv, meta.EachListItem(obj, emit)
}

// stream reads the list as it arrives. It fails with errListFormat, before
// or after handing items over, when the list has to be read whole instead.
func (t *typedLister) stream(ctx context.Context, opts metav1.ListOptions, each func(runtime.Object) error) (string, error) {
	kind, ok := typedKinds[t.gvr.Group][t.gvr.Resource]
	if !ok {
		return "", errListFormat
	}
	rc := usableREST(kind.client(t.client))
	if rc == nil {
		return "", errListFormat
	}
	req := rc.Get().Resource(t.gvr.Resource).VersionedParams(&opts, scheme.ParameterCodec)
	if !kind.clusterScoped {
		req = req.Namespace(t.ns)
	}
	body, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer body.Close()
	rv, err := decodeProtoList(body, kind.item, each)
	if errors.Is(err, errListFormat) {
		log.Printf("k8s watcher: %s list cannot be read as a stream (%v), decoding it whole", t.gvr.Resource, err)
	}
	return rv, err
}

// asListed puts a typed object in the form list rows are built from: a kind
// with a typed adapter stays typed, any other is slimmed and converted to
// unstructured. The object is changed in place.
func asListed(obj runtime.Object) runtime.Object {
	if _, already := obj.(*unstructured.Unstructured); already || listadapters.HasTypedAdapter(obj) {
		return obj
	}
	slimTyped(obj)
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return obj
	}
	return &unstructured.Unstructured{Object: m}
}

// slimTyped drops what no list adapter reads before an object goes through the
// reflection conversion to unstructured: managedFields (a JSON field tree per
// manager), pod templates, node image lists and Secret and ConfigMap values,
// of which the adapters only count the keys. Converting those was most of the
// cost of a list (5,000 Deployments: about 940 ms down to 90 ms) for rows that
// come out the same. The object was just decoded for this lister, so it is
// changed in place.
func slimTyped(obj runtime.Object) {
	if a, err := meta.Accessor(obj); err == nil {
		a.SetManagedFields(nil)
	}
	switch o := obj.(type) {
	case *appsv1.Deployment:
		o.Spec.Template = corev1.PodTemplateSpec{}
	case *appsv1.ReplicaSet:
		o.Spec.Template = corev1.PodTemplateSpec{}
	case *appsv1.StatefulSet:
		o.Spec.Template = corev1.PodTemplateSpec{}
		o.Spec.VolumeClaimTemplates = nil
	case *appsv1.DaemonSet:
		o.Spec.Template = corev1.PodTemplateSpec{}
	case *batchv1.Job:
		o.Spec.Template = corev1.PodTemplateSpec{}
	case *batchv1.CronJob:
		o.Spec.JobTemplate = batchv1.JobTemplateSpec{}
	case *corev1.ReplicationController:
		o.Spec.Template = nil
	case *corev1.Node:
		o.Status.Images = nil
	case *corev1.Secret:
		for k := range o.Data {
			o.Data[k] = nil
		}
		o.StringData = nil
	case *corev1.ConfigMap:
		for k := range o.Data {
			o.Data[k] = ""
		}
		for k := range o.BinaryData {
			o.BinaryData[k] = nil
		}
	}
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
				ev.Object = asListed(ev.Object)
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

// typedKind is what reading a native kind's list as a stream takes: the client
// of its API group and an empty item to decode into.
type typedKind struct {
	client        func(kubernetes.Interface) rest.Interface
	item          func() protoItem
	clusterScoped bool
}

func coreV1(c kubernetes.Interface) rest.Interface        { return c.CoreV1().RESTClient() }
func appsV1(c kubernetes.Interface) rest.Interface        { return c.AppsV1().RESTClient() }
func batchV1(c kubernetes.Interface) rest.Interface       { return c.BatchV1().RESTClient() }
func networkingV1(c kubernetes.Interface) rest.Interface  { return c.NetworkingV1().RESTClient() }
func rbacV1(c kubernetes.Interface) rest.Interface        { return c.RbacV1().RESTClient() }
func storageV1(c kubernetes.Interface) rest.Interface     { return c.StorageV1().RESTClient() }
func policyV1(c kubernetes.Interface) rest.Interface      { return c.PolicyV1().RESTClient() }
func autoscalingV2(c kubernetes.Interface) rest.Interface { return c.AutoscalingV2().RESTClient() }

// typedKinds are the kinds typedList and typedWatch serve, in the API version
// those use.
var typedKinds = map[string]map[string]typedKind{
	"": {
		"pods":                   {coreV1, func() protoItem { return &corev1.Pod{} }, false},
		"services":               {coreV1, func() protoItem { return &corev1.Service{} }, false},
		"configmaps":             {coreV1, func() protoItem { return &corev1.ConfigMap{} }, false},
		"secrets":                {coreV1, func() protoItem { return &corev1.Secret{} }, false},
		"serviceaccounts":        {coreV1, func() protoItem { return &corev1.ServiceAccount{} }, false},
		"persistentvolumeclaims": {coreV1, func() protoItem { return &corev1.PersistentVolumeClaim{} }, false},
		"endpoints":              {coreV1, func() protoItem { return &corev1.Endpoints{} }, false},
		"nodes":                  {coreV1, func() protoItem { return &corev1.Node{} }, true},
		"namespaces":             {coreV1, func() protoItem { return &corev1.Namespace{} }, true},
		"persistentvolumes":      {coreV1, func() protoItem { return &corev1.PersistentVolume{} }, true},
		"replicationcontrollers": {coreV1, func() protoItem { return &corev1.ReplicationController{} }, false},
	},
	"apps": {
		"deployments":  {appsV1, func() protoItem { return &appsv1.Deployment{} }, false},
		"statefulsets": {appsV1, func() protoItem { return &appsv1.StatefulSet{} }, false},
		"daemonsets":   {appsV1, func() protoItem { return &appsv1.DaemonSet{} }, false},
		"replicasets":  {appsV1, func() protoItem { return &appsv1.ReplicaSet{} }, false},
	},
	"batch": {
		"jobs":     {batchV1, func() protoItem { return &batchv1.Job{} }, false},
		"cronjobs": {batchV1, func() protoItem { return &batchv1.CronJob{} }, false},
	},
	"networking.k8s.io": {
		"ingresses":       {networkingV1, func() protoItem { return &networkingv1.Ingress{} }, false},
		"networkpolicies": {networkingV1, func() protoItem { return &networkingv1.NetworkPolicy{} }, false},
		"ingressclasses":  {networkingV1, func() protoItem { return &networkingv1.IngressClass{} }, true},
	},
	"rbac.authorization.k8s.io": {
		"roles":               {rbacV1, func() protoItem { return &rbacv1.Role{} }, false},
		"rolebindings":        {rbacV1, func() protoItem { return &rbacv1.RoleBinding{} }, false},
		"clusterroles":        {rbacV1, func() protoItem { return &rbacv1.ClusterRole{} }, true},
		"clusterrolebindings": {rbacV1, func() protoItem { return &rbacv1.ClusterRoleBinding{} }, true},
	},
	"storage.k8s.io": {
		"storageclasses": {storageV1, func() protoItem { return &storagev1.StorageClass{} }, true},
		"csidrivers":     {storageV1, func() protoItem { return &storagev1.CSIDriver{} }, true},
		"csinodes":       {storageV1, func() protoItem { return &storagev1.CSINode{} }, true},
	},
	"policy": {
		"poddisruptionbudgets": {policyV1, func() protoItem { return &policyv1.PodDisruptionBudget{} }, false},
	},
	"autoscaling": {
		"horizontalpodautoscalers": {autoscalingV2, func() protoItem { return &autoscalingv2.HorizontalPodAutoscaler{} }, false},
	},
}

func isNativeKind(gvr schema.GroupVersionResource) bool {
	_, ok := typedKinds[gvr.Group][gvr.Resource]
	return ok
}
