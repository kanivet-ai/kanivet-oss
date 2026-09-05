package listadapters

import (
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// PresimplifiedKey marks an UnstructuredList whose items are already simplified
// maps produced by SimplifyTyped, so callers skip the unstructured adapters.
const PresimplifiedKey = "__kanivet_presimplified"

// HasTypedAdapter reports whether obj can be simplified straight from its typed
// form, skipping the reflection-based conversion to unstructured.
func HasTypedAdapter(obj runtime.Object) bool {
	_, ok := obj.(*v1.Pod)
	return ok
}

func SimplifyTyped(obj runtime.Object, gvr schema.GroupVersionResource) (map[string]interface{}, bool) {
	if p, ok := obj.(*v1.Pod); ok && p != nil {
		return typedPod(p, gvr), true
	}
	return nil, false
}

func TypedList(obj runtime.Object, gvr schema.GroupVersionResource) (*unstructured.UnstructuredList, bool) {
	pl, ok := obj.(*v1.PodList)
	if !ok {
		return nil, false
	}
	ul := &unstructured.UnstructuredList{Object: map[string]interface{}{
		PresimplifiedKey: true,
		"metadata":       map[string]interface{}{"resourceVersion": pl.ResourceVersion, "continue": pl.Continue},
	}}
	ul.Items = make([]unstructured.Unstructured, len(pl.Items))
	for i := range pl.Items {
		ul.Items[i].Object = typedPod(&pl.Items[i], gvr)
	}
	return ul, true
}

func IsPresimplified(l *unstructured.UnstructuredList) bool {
	v, _ := l.Object[PresimplifiedKey].(bool)
	return v
}

var minimalPodKeys = []string{"name", "namespace", "uid", "creationTimestamp", "deletionTimestamp", "resourceVersion", "kind", "apiVersion", "phase", "containerStatuses", "restarts", "initContainerStatuses", "containers"}

func MinimalProjection(item map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(minimalPodKeys))
	for _, k := range minimalPodKeys {
		if v, ok := item[k]; ok {
			out[k] = v
		}
	}
	return out
}

func toMap(v interface{}) map[string]interface{} {
	m, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(v)
	return m
}

func stringMap(m map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func typedPod(p *v1.Pod, gvr schema.GroupVersionResource) map[string]interface{} {
	item := map[string]interface{}{"name": p.Name, "uid": string(p.UID), "apiVersion": p.APIVersion}
	if p.Namespace != "" {
		item["namespace"] = p.Namespace
	}
	if !p.CreationTimestamp.IsZero() {
		item["creationTimestamp"] = p.CreationTimestamp.UTC().Format(time.RFC3339)
	}
	if p.DeletionTimestamp != nil && !p.DeletionTimestamp.IsZero() {
		item["deletionTimestamp"] = p.DeletionTimestamp.UTC().Format(time.RFC3339)
	}
	if len(p.Labels) > 0 {
		item["labels"] = stringMap(p.Labels)
	}
	if len(p.Annotations) > 0 {
		item["annotations"] = stringMap(p.Annotations)
	}
	if len(p.OwnerReferences) > 0 {
		refs := make([]interface{}, len(p.OwnerReferences))
		for i := range p.OwnerReferences {
			refs[i] = toMap(&p.OwnerReferences[i])
		}
		item["ownerReferences"] = refs
	}
	if p.ResourceVersion != "" {
		item["resourceVersion"] = p.ResourceVersion
	}
	if p.Kind != "" {
		item["kind"] = p.Kind
	} else if gvr.Resource != "" {
		item["kind"] = gvr.Resource
	}
	if p.Status.Phase != "" {
		item["phase"] = string(p.Status.Phase)
	}
	if len(p.Status.Conditions) > 0 {
		conds := make([]interface{}, len(p.Status.Conditions))
		for i := range p.Status.Conditions {
			conds[i] = toMap(&p.Status.Conditions[i])
		}
		item["conditions"] = conds
	}
	if len(p.Status.ContainerStatuses) > 0 {
		item["containerStatuses"] = compactTypedStatuses(p.Status.ContainerStatuses)
		restarts := 0
		for i := range p.Status.ContainerStatuses {
			restarts += int(p.Status.ContainerStatuses[i].RestartCount)
		}
		item["restarts"] = restarts
	}
	if len(p.Status.InitContainerStatuses) > 0 {
		item["initContainerStatuses"] = compactTypedStatuses(p.Status.InitContainerStatuses)
	}
	if len(p.Spec.Containers) > 0 {
		names := make([]map[string]interface{}, len(p.Spec.Containers))
		for i := range p.Spec.Containers {
			names[i] = map[string]interface{}{"name": p.Spec.Containers[i].Name}
		}
		item["containers"] = names
	}
	if p.Spec.NodeName != "" {
		item["nodeName"] = p.Spec.NodeName
	}
	if p.Spec.ServiceAccountName != "" {
		item["serviceAccountName"] = p.Spec.ServiceAccountName
	}
	if p.Spec.PriorityClassName != "" {
		item["priorityClassName"] = p.Spec.PriorityClassName
	}
	return item
}

func compactTypedStatuses(statuses []v1.ContainerStatus) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(statuses))
	for i := range statuses {
		cs := &statuses[i]
		out = append(out, map[string]interface{}{"name": cs.Name, "state": toMap(&cs.State), "ready": cs.Ready, "restartCount": int64(cs.RestartCount)})
	}
	return out
}
