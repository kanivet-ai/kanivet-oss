package argo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	clusterSecretLabelKey = "argocd.argoproj.io/secret-type"
	clusterSecretLabelVal = "cluster"
)

type DestinationKind string

const (
	DestinationLocal    DestinationKind = "local"
	DestinationVCluster DestinationKind = "vcluster"
	DestinationExternal DestinationKind = "external"
)

type VClusterRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type Destination struct {
	Kind     DestinationKind `json:"kind"`
	Server   string          `json:"server,omitempty"`
	Name     string          `json:"name,omitempty"`
	VCluster *VClusterRef    `json:"vcluster,omitempty"`
}

type VClusterEntry struct {
	Namespace string
	Name      string
}

type DestinationMap struct {
	ByName   map[string]Destination `json:"byName"`
	ByServer map[string]Destination `json:"byServer"`
}

func BuildDestinationMap(ctx context.Context, kube kubernetes.Interface, argoNamespace string, vclusters []VClusterEntry) (*DestinationMap, error) {
	if argoNamespace == "" {
		argoNamespace = defaultArgoNamespace
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	out := &DestinationMap{
		ByName:   map[string]Destination{},
		ByServer: map[string]Destination{},
	}

	caFingerprintToVCluster := map[string]VClusterRef{}
	serverToVCluster := map[string]VClusterRef{}

	for _, vc := range vclusters {
		secret, err := kube.CoreV1().Secrets(vc.Namespace).Get(timeoutCtx, "vc-"+vc.Name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		raw, ok := secret.Data["config"]
		if !ok || len(raw) == 0 {
			continue
		}
		apiCfg, err := clientcmd.Load(raw)
		if err != nil {
			continue
		}
		ref := VClusterRef{Namespace: vc.Namespace, Name: vc.Name}
		for _, cluster := range apiCfg.Clusters {
			if cluster == nil {
				continue
			}
			if len(cluster.CertificateAuthorityData) > 0 {
				caFingerprintToVCluster[fingerprint(cluster.CertificateAuthorityData)] = ref
			}
			if cluster.Server != "" {
				serverToVCluster[normalizeServer(cluster.Server)] = ref
			}
		}
	}

	secrets, err := kube.CoreV1().Secrets(argoNamespace).List(timeoutCtx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", clusterSecretLabelKey, clusterSecretLabelVal),
	})
	if err != nil {
		return out, fmt.Errorf("list argo cluster secrets: %w", err)
	}

	for i := range secrets.Items {
		s := &secrets.Items[i]
		name := string(s.Data["name"])
		server := string(s.Data["server"])
		if name == "" && server == "" {
			continue
		}
		dest := classifySecret(s, server, caFingerprintToVCluster, serverToVCluster)
		dest.Name = name
		dest.Server = server
		if name != "" {
			out.ByName[name] = dest
		}
		if server != "" {
			out.ByServer[normalizeServer(server)] = dest
		}
	}

	return out, nil
}

func classifySecret(s *corev1.Secret, server string, caMap map[string]VClusterRef, serverMap map[string]VClusterRef) Destination {
	if isLocalServer(server) {
		return Destination{Kind: DestinationLocal}
	}

	configRaw := s.Data["config"]
	if len(configRaw) > 0 {
		if ref, ok := matchByConfig(configRaw, caMap); ok {
			return Destination{Kind: DestinationVCluster, VCluster: &ref}
		}
	}

	if server != "" {
		if ref, ok := serverMap[normalizeServer(server)]; ok {
			return Destination{Kind: DestinationVCluster, VCluster: &ref}
		}
	}

	return Destination{Kind: DestinationExternal}
}

func matchByConfig(raw []byte, caMap map[string]VClusterRef) (VClusterRef, bool) {
	type clusterConfig struct {
		TLSClientConfig struct {
			CAData []byte `json:"caData"`
		} `json:"tlsClientConfig"`
	}
	var cfg clusterConfig
	if err := json.Unmarshal(raw, &cfg); err == nil && len(cfg.TLSClientConfig.CAData) > 0 {
		if ref, ok := caMap[fingerprint(cfg.TLSClientConfig.CAData)]; ok {
			return ref, true
		}
	}
	return VClusterRef{}, false
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func normalizeServer(s string) string {
	s = strings.TrimRight(s, "/")
	parsed, err := url.Parse(s)
	if err != nil {
		return s
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return fmt.Sprintf("%s://%s:%s%s", parsed.Scheme, host, port, parsed.Path)
}
