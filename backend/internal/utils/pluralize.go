package utils

import "strings"

// irregularPlurals lists kinds whose API resource name the suffix rules below
// would not produce. Discovery is the authority on resource names; this table
// and the rules are the fallback for kinds discovery has not described.
var irregularPlurals = map[string]string{
	"endpoints": "endpoints",
}

// PluralizeKind returns the lowercase plural resource name for a Kubernetes
// kind the way the API server spells it: Deployment becomes deployments,
// NetworkPolicy becomes networkpolicies, Ingress becomes ingresses, Gateway
// becomes gateways, Prometheus becomes prometheuses.
//
// It is idempotent on resource names, so callers may pass whichever spelling
// they hold: pods stays pods, ingresses stays ingresses.
func PluralizeKind(kind string) string {
	lower := strings.ToLower(strings.TrimSpace(kind))
	if lower == "" {
		return lower
	}
	if p, ok := irregularPlurals[lower]; ok {
		return p
	}
	switch {
	case strings.HasSuffix(lower, "ss"), strings.HasSuffix(lower, "us"):
		// ingress, storageclass, ipaddress, prometheus, componentstatus
		return lower + "es"
	case strings.HasSuffix(lower, "s"):
		// Already a resource name (pods, gateways, statuses). Kinds ending in a
		// bare s are rare enough that treating the input as plural is the
		// safer reading.
		return lower
	case strings.HasSuffix(lower, "x"), strings.HasSuffix(lower, "z"),
		strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "sh"):
		return lower + "es"
	case strings.HasSuffix(lower, "y") && len(lower) > 1 && !isVowel(lower[len(lower)-2]):
		// policy -> policies, but gateway -> gateways
		return lower[:len(lower)-1] + "ies"
	}
	return lower + "s"
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}
