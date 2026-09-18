package search

import "testing"

func TestSingularize(t *testing.T) {
	cases := map[string]string{
		"pods":              "pod",
		"ingresses":         "ingress",
		"storageclasses":    "storageclass",
		"networkpolicies":   "networkpolicy",
		"leases":            "lease",
		"componentstatuses": "componentstatus",
		"endpoints":         "endpoints",
		"endpointslices":    "endpointslice",
		"gateways":          "gateway",
		"ingress":           "ingress",
	}
	for plural, want := range cases {
		if got := singularize(plural); got != want {
			t.Errorf("singularize(%q) = %q, want %q", plural, got, want)
		}
	}
}

func TestGetCategoryForKindAcceptsEitherSpelling(t *testing.T) {
	for _, kind := range []string{"Ingress", "ingresses", "Lease", "leases", "StorageClass", "storageclasses"} {
		if got := getCategoryForKind(kind); got == "Other" {
			t.Errorf("getCategoryForKind(%q) = Other", kind)
		}
	}
}
