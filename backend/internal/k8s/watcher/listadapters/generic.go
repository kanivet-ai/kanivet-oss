package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func generic(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "phase", "conditions")
	}
	return item
}
