package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	apps := "apps"
	register(schema.GroupVersionResource{Group: apps, Version: "v1", Resource: "deployments"}, replicated)
	register(schema.GroupVersionResource{Group: apps, Version: "v1", Resource: "replicasets"}, replicated)
	register(schema.GroupVersionResource{Group: apps, Version: "v1", Resource: "statefulsets"}, replicated)
	register(schema.GroupVersionResource{Group: apps, Version: "v1", Resource: "daemonsets"}, daemonset)
	batch := "batch"
	register(schema.GroupVersionResource{Group: batch, Version: "v1", Resource: "jobs"}, job)
	register(schema.GroupVersionResource{Group: batch, Version: "v1", Resource: "cronjobs"}, cronjob)
}

func replicated(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object

	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "readyReplicas", "updatedReplicas", "availableReplicas", "conditions")
		if r, ok := status["replicas"]; ok {
			item["statusReplicas"] = r
		}
	}

	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "replicas")
		if sel, ok := spec["selector"]; ok {
			item["selector"] = sel
		}
	}

	if meta := getMap(obj, "metadata"); meta != nil {
		if ann, ok := meta["annotations"].(map[string]interface{}); ok {
			if rev, ok := ann["deployment.kubernetes.io/revision"]; ok {
				item["annotations"] = map[string]interface{}{"deployment.kubernetes.io/revision": rev}
			} else {
				delete(item, "annotations")
			}
		}
	}

	return item
}

func daemonset(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object

	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status,
			"desiredNumberScheduled", "currentNumberScheduled", "numberReady",
			"updatedNumberScheduled", "numberAvailable", "conditions",
		)
	}

	if spec := getMap(obj, "spec"); spec != nil {
		if sel, ok := spec["selector"]; ok {
			item["selector"] = sel
		}
	}

	return item
}

func job(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object

	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "conditions", "startTime", "completionTime", "succeeded", "failed", "active")
	}

	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "completions", "parallelism", "backoffLimit")
	}

	return item
}

func cronjob(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object

	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "schedule", "suspend")
	}

	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "active", "lastScheduleTime", "lastSuccessfulTime")
	}

	return item
}
