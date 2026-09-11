package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Adapter func(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{}

var registry = map[string]Adapter{}

func register(gvr schema.GroupVersionResource, a Adapter) {
	registry[key(gvr)] = a
}

func key(gvr schema.GroupVersionResource) string {
	return gvr.Group + "/" + gvr.Resource
}

func HasAdapter(gvr schema.GroupVersionResource) bool {
	_, ok := registry[key(gvr)]
	return ok
}

func Simplify(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	if a, ok := registry[key(gvr)]; ok {
		return a(u, gvr)
	}
	return generic(u, gvr)
}

func baseMeta(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	obj := u.Object
	item := map[string]interface{}{}
	if meta, _ := obj["metadata"].(map[string]interface{}); meta != nil {
		setStr(item, meta, "name", "name")
		setStr(item, meta, "namespace", "namespace")
		setStr(item, meta, "uid", "uid")
		setStr(item, meta, "creationTimestamp", "creationTimestamp")
		setStr(item, meta, "deletionTimestamp", "deletionTimestamp")
		if v, ok := meta["labels"]; ok {
			item["labels"] = v
		}
		if v, ok := meta["annotations"]; ok {
			item["annotations"] = v
		}
		if v, ok := meta["ownerReferences"]; ok {
			item["ownerReferences"] = v
		}
	}
	if rv := u.GetResourceVersion(); rv != "" {
		item["resourceVersion"] = rv
	}
	if kind := u.GetKind(); kind != "" {
		item["kind"] = kind
	} else if gvr.Resource != "" {
		item["kind"] = gvr.Resource
	}
	item["apiVersion"] = u.GetAPIVersion()
	return item
}

func setStr(dst, src map[string]interface{}, srcKey, dstKey string) {
	if v, ok := src[srcKey]; ok && v != nil {
		dst[dstKey] = v
	}
}

func copyFields(dst, src map[string]interface{}, keys ...string) {
	for _, k := range keys {
		if v, ok := src[k]; ok {
			dst[k] = v
		}
	}
}

func getMap(obj map[string]interface{}, key string) map[string]interface{} {
	if m, ok := obj[key].(map[string]interface{}); ok {
		return m
	}
	return nil
}

func getSlice(obj map[string]interface{}, key string) []interface{} {
	if s, ok := obj[key].([]interface{}); ok {
		return s
	}
	return nil
}

func sumRestarts(statuses []interface{}) int {
	total := 0
	for _, v := range statuses {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		switch t := m["restartCount"].(type) {
		case int64:
			total += int(t)
		case int:
			total += t
		case float64:
			total += int(t)
		}
	}
	return total
}
