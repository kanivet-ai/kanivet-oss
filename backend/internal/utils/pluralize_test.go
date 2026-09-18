package utils

import "testing"

func TestPluralizeKind(t *testing.T) {
	cases := map[string]string{
		"Pod":                      "pods",
		"Deployment":               "deployments",
		"CronJob":                  "cronjobs",
		"Ingress":                  "ingresses",
		"IngressClass":             "ingressclasses",
		"StorageClass":             "storageclasses",
		"PriorityClass":            "priorityclasses",
		"IPAddress":                "ipaddresses",
		"NetworkPolicy":            "networkpolicies",
		"PodSecurityPolicy":        "podsecuritypolicies",
		"Gateway":                  "gateways",
		"GatewayClass":             "gatewayclasses",
		"HTTPRoute":                "httproutes",
		"Lease":                    "leases",
		"Endpoints":                "endpoints",
		"EndpointSlice":            "endpointslices",
		"ComponentStatus":          "componentstatuses",
		"Prometheus":               "prometheuses",
		"HorizontalPodAutoscaler":  "horizontalpodautoscalers",
		"CustomResourceDefinition": "customresourcedefinitions",
		"":                         "",
	}
	for kind, want := range cases {
		if got := PluralizeKind(kind); got != want {
			t.Errorf("PluralizeKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

// Resource names must survive a second pass unchanged so callers may hand
// PluralizeKind whichever spelling they hold.
func TestPluralizeKindIsIdempotentOnResourceNames(t *testing.T) {
	for _, kind := range []string{"Pod", "Ingress", "Gateway", "NetworkPolicy", "Prometheus", "StorageClass", "Endpoints", "Lease"} {
		once := PluralizeKind(kind)
		if twice := PluralizeKind(once); twice != once {
			t.Errorf("PluralizeKind(%q) = %q but PluralizeKind(%q) = %q", kind, once, once, twice)
		}
	}
}
