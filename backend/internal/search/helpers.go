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
	"lease":                   "Cluster",
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

// singularize maps a lowercase plural resource name back to its kind, the
// inverse of utils.PluralizeKind: ingresses -> ingress, policies -> policy,
// leases -> lease, componentstatuses -> componentstatus.
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
	// Only -sses/-uses/-xes/-zes/-ches/-shes drop an "es"; other -ses words
	// (leases, releases) end in a silent e and drop just the "s".
	for _, suffix := range []string{"sses", "uses", "xes", "zes", "ches", "shes"} {
		if strings.HasSuffix(kind, suffix) {
			return kind[:len(kind)-2]
		}
	}
	if strings.HasSuffix(kind, "s") && !strings.HasSuffix(kind, "ss") {
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
