package search

import (
	"sort"
	"strings"
)

var kindCategories = map[string]string{
	"pod":                     "Workloads",
	"deployment":              "Workloads",
	"replicaset":              "Workloads",
	"statefulset":             "Workloads",
	"daemonset":               "Workloads",
	"job":                     "Workloads",
	"cronjob":                 "Workloads",
	"service":                 "Networking",
	"ingress":                 "Networking",
	"ingressclass":            "Networking",
	"endpoints":               "Networking",
	"endpointslice":           "Networking",
	"networkpolicy":           "Networking",
	"configmap":               "Configuration",
	"secret":                  "Configuration",
	"persistentvolume":        "Storage",
	"persistentvolumeclaim":   "Storage",
	"storageclass":            "Storage",
	"volumeattachment":        "Storage",
	"namespace":               "Cluster",
	"node":                    "Cluster",
	"componentstatus":         "Cluster",
	"event":                   "Cluster",
	"serviceaccount":          "Security",
	"role":                    "Security",
	"rolebinding":             "Security",
	"clusterrole":             "Security",
	"clusterrolebinding":      "Security",
	"podsecuritypolicy":       "Security",
	"horizontalpodautoscaler": "Autoscaling",
	"verticalpodautoscaler":   "Autoscaling",
	"poddisruptionbudget":     "Policy",
	"limitrange":              "Policy",
	"resourcequota":           "Policy",
}

func singularize(kind string) string {
	if kind == "" {
		return kind
	}
	switch kind {
	case "endpoints", "componentstatus":
		return kind
	}
	if strings.HasSuffix(kind, "ies") {
		return kind[:len(kind)-3] + "y"
	}
	if strings.HasSuffix(kind, "ses") || strings.HasSuffix(kind, "xes") || strings.HasSuffix(kind, "ches") || strings.HasSuffix(kind, "shes") {
		return kind[:len(kind)-2]
	}
	if strings.HasSuffix(kind, "s") {
		return kind[:len(kind)-1]
	}
	return kind
}

func getCategoryForKind(kind string) string {
	k := strings.ToLower(kind)
	if category, exists := kindCategories[k]; exists {
		return category
	}
	if category, exists := kindCategories[singularize(k)]; exists {
		return category
	}
	return "Other"
}

func hasVerb(vs []string, want string) bool {
	for _, v := range vs {
		if v == want {
			return true
		}
	}
	return false
}

func getMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
