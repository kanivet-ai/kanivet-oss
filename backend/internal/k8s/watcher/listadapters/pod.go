package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, pod)
}

func pod(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object

	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "phase", "conditions")
		if cs := getSlice(status, "containerStatuses"); cs != nil {
			item["containerStatuses"] = compactContainerStatuses(cs)
			item["restarts"] = sumRestarts(cs)
		}
		if ics := getSlice(status, "initContainerStatuses"); ics != nil {
			item["initContainerStatuses"] = compactContainerStatuses(ics)
		}
	}

	if spec := getMap(obj, "spec"); spec != nil {
		if containers := getSlice(spec, "containers"); containers != nil {
			item["containers"] = containerNames(containers)
		}
		copyFields(item, spec, "nodeName", "serviceAccountName", "priorityClassName")
	}

	return item
}

func compactContainerStatuses(raw []interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		slim := map[string]interface{}{"name": m["name"], "state": m["state"], "ready": m["ready"]}
		if rc, ok := m["restartCount"]; ok {
			slim["restartCount"] = rc
		}
		out = append(out, slim)
	}
	return out
}

func containerNames(raw []interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, map[string]interface{}{"name": m["name"]})
	}
	return out
}
