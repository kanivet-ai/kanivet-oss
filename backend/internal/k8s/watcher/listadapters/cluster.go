package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}, node)
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}, namespace)
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}, event)
	register(schema.GroupVersionResource{Group: "events.k8s.io", Version: "v1", Resource: "events"}, event)
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}, serviceaccount)
	register(schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"}, lease)
	register(schema.GroupVersionResource{Group: "node.k8s.io", Version: "v1", Resource: "runtimeclasses"}, runtimeclass)
	register(schema.GroupVersionResource{Group: "certificates.k8s.io", Version: "v1", Resource: "certificatesigningrequests"}, certificatesigningrequest)
	register(schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}, apiservice)
	register(schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations"}, webhookConfig)
	register(schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"}, webhookConfig)
	register(schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}, poddisruptionbudget)
	register(schema.GroupVersionResource{Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses"}, priorityclass)
	rbac := "rbac.authorization.k8s.io"
	register(schema.GroupVersionResource{Group: rbac, Version: "v1", Resource: "roles"}, generic)
	register(schema.GroupVersionResource{Group: rbac, Version: "v1", Resource: "clusterroles"}, generic)
	register(schema.GroupVersionResource{Group: rbac, Version: "v1", Resource: "rolebindings"}, roleBinding)
	register(schema.GroupVersionResource{Group: rbac, Version: "v1", Resource: "clusterrolebindings"}, roleBinding)
}

func node(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "taints", "unschedulable")
	}
	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "conditions", "addresses", "nodeInfo", "capacity", "allocatable")
	}
	return item
}

func namespace(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if status := getMap(u.Object, "status"); status != nil {
		copyFields(item, status, "phase")
	}
	return item
}

func event(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if u.GetKind() == "" {
		item["kind"] = "Event"
	}
	obj := u.Object
	for _, k := range []string{"involvedObject", "source", "message", "reason", "type", "count", "firstTimestamp", "lastTimestamp", "eventTime"} {
		if v, ok := obj[k]; ok {
			item[k] = v
		}
	}
	return item
}

func serviceaccount(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	copyFields(item, obj, "secrets", "automountServiceAccountToken")
	return item
}

func lease(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "holderIdentity")
	}
	return item
}

func runtimeclass(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if handler, ok := u.Object["handler"]; ok {
		item["handler"] = handler
	}
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "handler")
	}
	return item
}

func certificatesigningrequest(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "signerName", "username")
	}
	if status := getMap(u.Object, "status"); status != nil {
		copyFields(item, status, "conditions")
	}
	return item
}

func apiservice(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "service", "group", "version")
	}
	if status := getMap(u.Object, "status"); status != nil {
		copyFields(item, status, "conditions")
	}
	return item
}

func webhookConfig(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if webhooks, ok := u.Object["webhooks"]; ok {
		item["webhooks"] = webhooks
	}
	return item
}

func poddisruptionbudget(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "minAvailable", "maxUnavailable")
	}
	if status := getMap(u.Object, "status"); status != nil {
		copyFields(item, status, "disruptionsAllowed")
	}
	return item
}

func priorityclass(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	copyFields(item, obj, "value", "globalDefault", "preemptionPolicy", "description")
	return item
}

func roleBinding(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if ref, ok := u.Object["roleRef"].(map[string]interface{}); ok {
		item["roleRef"] = ref
	}
	if subjects, ok := u.Object["subjects"]; ok {
		item["subjects"] = subjects
	}
	return item
}
