package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumes"}, persistentvolume)
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}, persistentvolumeclaim)
	register(schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}, storageclass)
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}, configmap)
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, secret)
}

func persistentvolume(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "capacity", "accessModes", "storageClassName", "persistentVolumeReclaimPolicy", "claimRef")
	}
	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "phase")
	}
	return item
}

func persistentvolumeclaim(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "accessModes", "storageClassName", "volumeName")
	}
	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "phase", "capacity")
	}
	return item
}

func storageclass(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	copyFields(item, obj, "provisioner", "reclaimPolicy", "volumeBindingMode")
	return item
}

func configmap(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	dataCount := 0
	if data, ok := obj["data"].(map[string]interface{}); ok {
		dataCount = len(data)
	}
	item["dataCount"] = dataCount
	if bd, ok := obj["binaryData"].(map[string]interface{}); ok {
		item["binaryDataCount"] = len(bd)
	}
	return item
}

func secret(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := configmap(u, gvr)
	if t, ok := u.Object["type"].(string); ok {
		item["type"] = t
	}
	return item
}
