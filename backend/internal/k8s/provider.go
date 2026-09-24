package k8s

import (
	"path/filepath"
	"strings"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// contextProvider tells where a context's cluster runs from its API server
// host, or failing that from its credential plugin, so a context renamed from
// its cloud default is still recognised.
func contextProvider(cfg *clientcmdapi.Config, name string) string {
	ctx, ok := cfg.Contexts[name]
	if !ok || ctx == nil {
		return ""
	}
	if cluster := cfg.Clusters[ctx.Cluster]; cluster != nil {
		server := strings.ToLower(cluster.Server)
		switch {
		case strings.Contains(server, ".eks.amazonaws.com"):
			return "aws"
		case strings.Contains(server, ".azmk8s.io"):
			return "azure"
		case strings.Contains(server, ".gke.io"), strings.Contains(server, "container.googleapis.com"):
			return "gcp"
		}
	}
	if auth := cfg.AuthInfos[ctx.AuthInfo]; auth != nil && auth.Exec != nil {
		switch filepath.Base(auth.Exec.Command) {
		case "aws", "aws-iam-authenticator":
			return "aws"
		case "gke-gcloud-auth-plugin":
			return "gcp"
		case "kubelogin":
			return "azure"
		}
	}
	return ""
}
