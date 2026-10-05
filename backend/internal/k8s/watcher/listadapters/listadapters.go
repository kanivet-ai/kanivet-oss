package listadapters

import (
	"strings"

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
			if ann := listAnnotations(v); ann != nil {
				item["annotations"] = ann
			}
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

// lastAppliedAnnotation is where kubectl apply keeps the whole applied
// manifest, Secret data included.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// omittedAnnotationPrefixes are other copies of the applied manifest (kapp,
// banzaicloud, Rancher's wrangler apply, gzipped) and the service account
// tokens vcluster stamps on synced objects and OpenShift on the dockercfg
// Secret of every service account.
var omittedAnnotationPrefixes = []string{
	"kapp.k14s.io/original",
	"banzaicloud.io/last-applied",
	"objectset.rio.cattle.io/applied",
	"vcluster.loft.sh/token-",
	"openshift.io/token-secret.value",
}

// omitAnnotation reports whether a list row leaves an annotation out. List
// rows go to the renderer, are filtered on every keystroke and are persisted
// as snapshots, so they never carry these blobs; the detail view still loads
// the full object.
func omitAnnotation(key string) bool {
	if key == lastAppliedAnnotation {
		return true
	}
	for _, p := range omittedAnnotationPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// listAnnotations returns the annotations a list row keeps. The object's own
// map is never modified: it is returned as is when nothing is omitted, and a
// filtered copy otherwise (nil when nothing is left).
func listAnnotations(v interface{}) interface{} {
	ann, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	omit := false
	for k := range ann {
		if omitAnnotation(k) {
			omit = true
			break
		}
	}
	if !omit {
		return ann
	}
	out := make(map[string]interface{}, len(ann))
	for k, val := range ann {
		if !omitAnnotation(k) {
			out[k] = val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
