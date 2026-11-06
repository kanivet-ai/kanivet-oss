// Package clusterinfo works out what kind of cluster Kanivet is talking to:
// which provider or distribution runs it, which Kubernetes version, and
// whether the control plane is managed (and therefore invisible). Detection
// combines several weak signals into a confidence score and keeps every
// signal as evidence, so the UI can show why it decided.
package clusterinfo

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Provider identifiers.
const (
	EKS           = "eks"
	GKE           = "gke"
	AKS           = "aks"
	OpenShift     = "openshift"
	K3s           = "k3s"
	RKE2          = "rke2"
	Kind          = "kind"
	Minikube      = "minikube"
	DockerDesktop = "docker-desktop"
	Kubeadm       = "kubeadm"
	Unknown       = "unknown"
)

// Providers lists every provider the detector knows, for override pickers.
var Providers = []string{EKS, GKE, AKS, OpenShift, K3s, RKE2, Kind, Minikube, DockerDesktop, Kubeadm}

var displayName = map[string]string{
	EKS: "EKS", GKE: "GKE", AKS: "AKS", OpenShift: "OpenShift", K3s: "k3s", RKE2: "RKE2",
	Kind: "kind", Minikube: "minikube", DockerDesktop: "Docker Desktop", Kubeadm: "kubeadm", Unknown: "Unknown",
}

// DisplayName is the provider's human name.
func DisplayName(p string) string {
	if n, ok := displayName[p]; ok {
		return n
	}
	return p
}

// managed providers run the control plane for you: its flags and files are
// the provider's responsibility and cannot be read from the API.
var managed = map[string]bool{EKS: true, GKE: true, AKS: true}

// Evidence is one signal the detector read.
type Evidence struct {
	// Signal names the source: providerID, nodeLabel, gitVersion, apiHost,
	// apiGroup, staticPod, nodeName.
	Signal string `json:"signal"`
	Value  string `json:"value"`
	// Provider is what the signal points to; Weight how strongly.
	Provider string  `json:"provider"`
	Weight   float64 `json:"weight"`
}

// Info is the detection result.
type Info struct {
	Provider string `json:"provider"`
	// Distribution is the display name, with a variant when one is known
	// ("EKS (Fargate)", "GKE Autopilot").
	Distribution        string `json:"distribution"`
	Version             string `json:"version"`
	GitVersion          string `json:"gitVersion,omitempty"`
	ManagedControlPlane bool   `json:"managedControlPlane"`
	// ControlPlaneVisible is true when kube-apiserver static pods can be read.
	ControlPlaneVisible bool       `json:"controlPlaneVisible"`
	Variants            []string   `json:"variants,omitempty"`
	Evidence            []Evidence `json:"evidence"`
	// Confidence is 0..1.
	Confidence float64 `json:"confidence"`
	// Overridden is true when the user picked the provider by hand.
	Overridden bool `json:"overridden,omitempty"`
}

// Signals is what detection reads. It is plain data so detection is tested
// without a cluster.
type Signals struct {
	Nodes      []corev1.Node
	GitVersion string
	APIHost    string
	// APIGroups are the served group names.
	APIGroups []string
	// KubeSystemPods are pod names in kube-system.
	KubeSystemPods []string
}

var (
	reEKSVersion  = regexp.MustCompile(`-eks-`)
	reGKEVersion  = regexp.MustCompile(`-gke\.`)
	reVersionCore = regexp.MustCompile(`^v?(\d+)\.(\d+)`)
)

// Detect scores every provider from the signals.
func Detect(s Signals) Info {
	var ev []Evidence
	add := func(signal, value, provider string, w float64) {
		ev = append(ev, Evidence{Signal: signal, Value: value, Provider: provider, Weight: w})
	}

	// providerID: the cloud controller sets it, one signal per prefix.
	seenID := map[string]bool{}
	for _, n := range s.Nodes {
		id := n.Spec.ProviderID
		var p string
		switch {
		case strings.HasPrefix(id, "aws://"):
			p = "aws"
		case strings.HasPrefix(id, "gce://"):
			p = GKE
		case strings.HasPrefix(id, "azure://"):
			p = AKS
		case strings.HasPrefix(id, "kind://"):
			p = Kind
		case strings.HasPrefix(id, "k3s://"):
			p = K3s
		}
		if p != "" && !seenID[p] {
			seenID[p] = true
			prefix := id
			if i := strings.Index(id, "://"); i >= 0 {
				prefix = id[:i+3]
			}
			if p == "aws" {
				// aws:// alone is EC2: kubeadm, k3s or EKS all run there.
				add("providerID", prefix, EKS, 1)
			} else {
				add("providerID", prefix, p, 2)
			}
		}
	}

	// Node labels.
	type lbl struct {
		key, provider string
		w             float64
		prefix        bool
	}
	labelSignals := []lbl{
		{"eks.amazonaws.com/", EKS, 2, true},
		{"karpenter.k8s.aws/", EKS, 1, true},
		{"cloud.google.com/gke-nodepool", GKE, 2, false},
		{"cloud.google.com/gke-", GKE, 1, true},
		{"kubernetes.azure.com/cluster", AKS, 2, false},
		{"kubernetes.azure.com/", AKS, 1, true},
		{"node.openshift.io/os_id", OpenShift, 2, false},
		{"minikube.k8s.io/", Minikube, 2, true},
		{"node.kubernetes.io/instance-type", "", 0, false},
	}
	seenLbl := map[string]bool{}
	for _, n := range s.Nodes {
		for k, v := range n.Labels {
			for _, l := range labelSignals {
				hit := k == l.key || (l.prefix && strings.HasPrefix(k, l.key))
				if !hit || seenLbl[l.key] {
					continue
				}
				seenLbl[l.key] = true
				if l.provider == "" {
					// instance-type: k3s writes "k3s", RKE2 "rke2"; otherwise cloud-only.
					switch v {
					case "k3s":
						add("nodeLabel", k+"="+v, K3s, 2)
					case "rke2":
						add("nodeLabel", k+"="+v, RKE2, 2)
					}
					continue
				}
				val := k
				if v != "" && !l.prefix {
					val = k + "=" + v
				}
				add("nodeLabel", val, l.provider, l.w)
			}
			if strings.HasPrefix(k, "rke2.io/") && !seenLbl["rke2.io/"] {
				seenLbl["rke2.io/"] = true
				add("nodeLabel", k, RKE2, 2)
			}
			if strings.HasPrefix(k, "k3s.io/") && !seenLbl["k3s.io/"] {
				seenLbl["k3s.io/"] = true
				add("nodeLabel", k, K3s, 2)
			}
		}
		// Annotations k3s and RKE2 set on every node.
		for k := range n.Annotations {
			if strings.HasPrefix(k, "rke2.io/") && !seenLbl["ann:rke2"] {
				seenLbl["ann:rke2"] = true
				add("nodeAnnotation", k, RKE2, 2)
			}
			if strings.HasPrefix(k, "k3s.io/") && !seenLbl["ann:k3s"] {
				seenLbl["ann:k3s"] = true
				add("nodeAnnotation", k, K3s, 1)
			}
		}
	}

	// Node names some local distributions use.
	for _, n := range s.Nodes {
		switch {
		case n.Name == "docker-desktop" || n.Name == "desktop-control-plane":
			add("nodeName", n.Name, DockerDesktop, 2)
		case n.Name == "minikube":
			add("nodeName", n.Name, Minikube, 1)
		}
	}

	// Server version.
	gv := s.GitVersion
	switch {
	case reEKSVersion.MatchString(gv):
		add("gitVersion", gv, EKS, 3)
	case reGKEVersion.MatchString(gv):
		add("gitVersion", gv, GKE, 3)
	case strings.Contains(gv, "+k3s"):
		add("gitVersion", gv, K3s, 3)
	case strings.Contains(gv, "+rke2"):
		add("gitVersion", gv, RKE2, 3)
	}

	// API server host.
	host := s.APIHost
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		host = u.Hostname()
	}
	switch {
	case strings.HasSuffix(host, ".eks.amazonaws.com") || strings.HasSuffix(host, ".eks.amazonaws.com.cn"):
		add("apiHost", host, EKS, 3)
	case strings.HasSuffix(host, ".azmk8s.io"):
		add("apiHost", host, AKS, 3)
	case strings.Contains(host, "gke") || strings.HasSuffix(host, ".googleapis.com"):
		add("apiHost", host, GKE, 1)
	case host == "kubernetes.docker.internal":
		add("apiHost", host, DockerDesktop, 2)
	}

	// OpenShift serves its config API group.
	for _, g := range s.APIGroups {
		if g == "config.openshift.io" {
			add("apiGroup", g, OpenShift, 3)
			break
		}
	}

	// Static control-plane pods: kube-apiserver-<node> means a self-managed
	// control plane whose flags can be read.
	cpVisible := false
	nodeNames := map[string]bool{}
	for _, n := range s.Nodes {
		nodeNames[n.Name] = true
	}
	for _, p := range s.KubeSystemPods {
		if rest, ok := strings.CutPrefix(p, "kube-apiserver-"); ok && (len(nodeNames) == 0 || nodeNames[rest]) {
			cpVisible = true
			add("staticPod", p, Kubeadm, 1)
			if strings.HasSuffix(rest, "-control-plane") {
				// kind names its control-plane node <cluster>-control-plane.
				add("nodeName", rest, Kind, 0.5)
			}
			break
		}
	}

	scores := map[string]float64{}
	var total float64
	for _, e := range ev {
		scores[e.Provider] += e.Weight
		total += e.Weight
	}
	// kubeadm is the fallback self-managed reading: any more specific
	// distribution that is also self-managed (kind, minikube) wins over it.
	best, bestScore := Unknown, 0.0
	keys := make([]string, 0, len(scores))
	for k := range scores {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sc := scores[k]
		if k == Kubeadm {
			continue
		}
		if sc > bestScore {
			best, bestScore = k, sc
		}
	}
	if best == Unknown && scores[Kubeadm] > 0 {
		best, bestScore = Kubeadm, scores[Kubeadm]
	}
	if best == Unknown && len(s.Nodes) > 0 {
		// Nothing specific: a plain self-managed cluster.
		best = Kubeadm
	}
	conf := 0.0
	if total > 0 {
		// Agreement (share of evidence for the winner, kubeadm signals count
		// for any self-managed winner) times strength (saturating at 4 points).
		agree := bestScore
		if best != Kubeadm && !managed[best] {
			agree += scores[Kubeadm]
		}
		strength := agree / 4
		if strength > 1 {
			strength = 1
		}
		conf = (agree / total) * strength
	} else if best == Kubeadm {
		conf = 0.2
	}
	info := Info{
		Provider:            best,
		Distribution:        DisplayName(best),
		Version:             minorVersion(gv),
		GitVersion:          gv,
		ManagedControlPlane: managed[best],
		ControlPlaneVisible: cpVisible,
		Evidence:            ev,
		Confidence:          round2(conf),
	}
	if info.Evidence == nil {
		info.Evidence = []Evidence{}
	}
	info.Variants = variants(s.Nodes, best)
	if len(info.Variants) > 0 {
		info.Distribution += " (" + strings.Join(info.Variants, ", ") + ")"
	}
	return info
}

func variants(nodes []corev1.Node, provider string) []string {
	var out []string
	fargate, autopilot := false, false
	for _, n := range nodes {
		if n.Labels["eks.amazonaws.com/compute-type"] == "fargate" || strings.HasPrefix(n.Name, "fargate-") {
			fargate = true
		}
		if _, ok := n.Labels["cloud.google.com/gke-autopilot"]; ok {
			autopilot = true
		}
		if n.Labels["cloud.google.com/gke-provisioning"] == "autopilot" {
			autopilot = true
		}
	}
	if provider == EKS && fargate {
		out = append(out, "Fargate")
	}
	if provider == GKE && autopilot {
		out = append(out, "Autopilot")
	}
	return out
}

func minorVersion(gv string) string {
	m := reVersionCore.FindStringSubmatch(gv)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

// ApplyOverride returns info with the provider replaced by a user choice.
// Evidence is kept so the UI can still show what was detected.
func ApplyOverride(info Info, provider string) Info {
	if provider == "" || provider == info.Provider {
		return info
	}
	info.Provider = provider
	info.Distribution = DisplayName(provider)
	info.ManagedControlPlane = managed[provider]
	info.Variants = nil
	info.Overridden = true
	return info
}

// IsManaged reports whether the provider runs the control plane.
func IsManaged(provider string) bool { return managed[provider] }

// Gather reads the signals from a cluster: nodes, the server version, API
// groups and kube-system pod names. Each read is best-effort: a forbidden
// node list leaves fewer signals, not an error.
func Gather(ctx context.Context, cs kubernetes.Interface, apiHost string, nodes []corev1.Node, kubeSystemPods []string) Signals {
	s := Signals{APIHost: apiHost, Nodes: nodes, KubeSystemPods: kubeSystemPods}
	if v, err := cs.Discovery().ServerVersion(); err == nil {
		s.GitVersion = v.GitVersion
	}
	if groups, err := cs.Discovery().ServerGroups(); err == nil {
		for _, g := range groups.Groups {
			s.APIGroups = append(s.APIGroups, g.Name)
		}
	}
	if s.Nodes == nil {
		if r, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{ResourceVersion: "0"}); err == nil {
			s.Nodes = r.Items
		}
	}
	if s.KubeSystemPods == nil {
		if r, err := cs.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{ResourceVersion: "0"}); err == nil {
			for _, p := range r.Items {
				s.KubeSystemPods = append(s.KubeSystemPods, p.Name)
			}
		}
	}
	return s
}
