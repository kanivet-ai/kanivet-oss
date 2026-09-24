package k8s

import (
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestContextProvider(t *testing.T) {
	cfg := clientcmdapi.NewConfig()
	add := func(name, server, command string) {
		cfg.Clusters[name] = &clientcmdapi.Cluster{Server: server}
		auth := &clientcmdapi.AuthInfo{}
		if command != "" {
			auth.Exec = &clientcmdapi.ExecConfig{Command: command}
		}
		cfg.AuthInfos[name] = auth
		cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	}
	add("sbx-aws-eu-north-1-2", "https://ABC.gr7.eu-north-1.eks.amazonaws.com", "aws")
	add("renamed-eks-behind-proxy", "https://10.0.0.1", "/usr/local/bin/aws-iam-authenticator")
	add("prod-gke", "https://34.1.2.3", "gke-gcloud-auth-plugin")
	add("aks", "https://aks-dns.hcp.westeurope.azmk8s.io:443", "kubelogin")
	add("kind-local", "https://127.0.0.1:6443", "")

	want := map[string]string{
		"sbx-aws-eu-north-1-2":     "aws",
		"renamed-eks-behind-proxy": "aws",
		"prod-gke":                 "gcp",
		"aks":                      "azure",
		"kind-local":               "",
		"missing":                  "",
	}
	for name, provider := range want {
		if got := contextProvider(cfg, name); got != provider {
			t.Errorf("%s: provider = %q, want %q", name, got, provider)
		}
	}
}
