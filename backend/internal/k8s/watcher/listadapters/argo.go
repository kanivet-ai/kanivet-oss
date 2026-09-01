package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	register(schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}, argoApplication)
	register(schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applicationsets"}, argoApplicationSet)
	register(schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "appprojects"}, argoAppProject)
}

func argoApplication(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	spec := getMap(obj, "spec")
	status := getMap(obj, "status")

	if spec != nil {
		if project, ok := spec["project"].(string); ok {
			item["project"] = project
		}
		if dest := getMap(spec, "destination"); dest != nil {
			if ns, ok := dest["namespace"].(string); ok {
				item["destNamespace"] = ns
			}
			if server, ok := dest["server"].(string); ok {
				item["destServer"] = server
			}
		}
		if src := getMap(spec, "source"); src != nil {
			if repo, ok := src["repoURL"].(string); ok {
				item["repoUrl"] = repo
			}
			if path, ok := src["path"].(string); ok {
				item["path"] = path
			}
			if rev, ok := src["targetRevision"].(string); ok {
				item["targetRevision"] = rev
			}
		}
	}

	syncStatus := ""
	healthStatus := ""
	if status != nil {
		if sync := getMap(status, "sync"); sync != nil {
			if s, ok := sync["status"].(string); ok {
				syncStatus = s
			}
			if rev, ok := sync["revision"].(string); ok {
				item["revision"] = rev
			}
		}
		if health := getMap(status, "health"); health != nil {
			if s, ok := health["status"].(string); ok {
				healthStatus = s
			}
		}
		if conds := getSlice(status, "conditions"); conds != nil {
			item["conditions"] = conds
		}
	}
	item["syncStatus"] = syncStatus
	item["healthStatus"] = healthStatus
	if healthStatus != "" {
		item["phase"] = healthStatus
	} else if syncStatus != "" {
		item["phase"] = syncStatus
	}
	return item
}

func argoApplicationSet(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if status := getMap(obj, "status"); status != nil {
		if conds := getSlice(status, "conditions"); conds != nil {
			item["conditions"] = conds
			for _, c := range conds {
				cm, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				t, _ := cm["type"].(string)
				s, _ := cm["status"].(string)
				if t == "ResourcesUpToDate" && s == "True" {
					item["phase"] = "Healthy"
				}
			}
		}
	}
	if _, ok := item["phase"]; !ok {
		item["phase"] = "Unknown"
	}
	return item
}

func argoAppProject(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		if desc, ok := spec["description"].(string); ok {
			item["description"] = desc
		}
	}
	item["phase"] = "Active"
	return item
}
