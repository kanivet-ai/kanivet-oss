package utils

import "strings"

func PluralizeKind(kind string) string {
	lower := strings.ToLower(kind)

	switch lower {
	case "ingress":
		return "ingresses"
	case "networkpolicy":
		return "networkpolicies"
	case "resourcequota":
		return "resourcequotas"
	case "limitrange":
		return "limitranges"
	case "podsecuritypolicy":
		return "podsecuritypolicies"
	case "priorityclass":
		return "priorityclasses"
	case "storageclass":
		return "storageclasses"
	case "ingressclass":
		return "ingressclasses"
	case "runtimeclass":
		return "runtimeclasses"
	case "poddisruptionbudget":
		return "poddisruptionbudgets"
	case "horizontalpodautoscaler":
		return "horizontalpodautoscalers"
	case "cronjob":
		return "cronjobs"
	case "daemonset":
		return "daemonsets"
	case "replicaset":
		return "replicasets"
	case "statefulset":
		return "statefulsets"
	case "endpoints":
		return "endpoints"
	case "endpointslice":
		return "endpointslices"
	}

	if strings.HasSuffix(lower, "s") {
		return lower
	}
	if strings.HasSuffix(lower, "y") && len(lower) > 1 {
		return lower[:len(lower)-1] + "ies"
	}
	if strings.HasSuffix(lower, "x") {
		return lower + "es"
	}

	return lower + "s"
}
