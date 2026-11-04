package topology

// appLabels are read in order; the first one set names the application.
var appLabels = []string{
	"app.kubernetes.io/name",
	"app.kubernetes.io/instance",
	"app.kubernetes.io/part-of",
	"app",
	"k8s-app",
}

// appOf names the application a resource belongs to, or "": the first
// application label set, then the Helm release that installed it.
func appOf(labels, annotations map[string]string) string {
	for _, k := range appLabels {
		if v := labels[k]; v != "" {
			return v
		}
	}
	if v := annotations["meta.helm.sh/release-name"]; v != "" {
		return v
	}
	return labels["release"]
}
