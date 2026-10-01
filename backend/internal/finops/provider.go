package finops

import (
	"regexp"
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
)

const (
	ProviderAWSEKS   = "AWS EKS"
	ProviderAWSEC2   = "AWS EC2"
	ProviderAzureAKS = "Azure AKS"
	ProviderGCPGKE   = "GCP GKE"
	ProviderUnknown  = "Unknown"
)

// eksControlPlaneHourly is the EKS standard-support cluster fee. AKS's free
// tier and self-managed clusters have no separate control plane charge.
const eksControlPlaneHourly = 0.10

var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(-gov)?-[a-z]+-\d$`)

// detectProvider reads the cloud from node providerIDs, which every cloud
// controller sets, and falls back to managed-service labels. Karpenter and
// self-managed EKS nodes carry no nodegroup label, so labels alone misreport them.
func detectProvider(nodes []v1.Node) string {
	eks := false
	for i := range nodes {
		for k := range nodes[i].Labels {
			if strings.HasPrefix(k, "eks.amazonaws.com/") || strings.HasPrefix(k, "karpenter.k8s.aws/") {
				eks = true
				break
			}
		}
		if eks {
			break
		}
	}
	for i := range nodes {
		id := nodes[i].Spec.ProviderID
		switch {
		case strings.HasPrefix(id, "aws://"):
			if eks {
				return ProviderAWSEKS
			}
			return ProviderAWSEC2
		case strings.HasPrefix(id, "azure://"):
			return ProviderAzureAKS
		case strings.HasPrefix(id, "gce://"):
			return ProviderGCPGKE
		}
	}
	for i := range nodes {
		labels := nodes[i].Labels
		if eks {
			return ProviderAWSEKS
		}
		if _, ok := labels["kubernetes.azure.com/cluster"]; ok {
			return ProviderAzureAKS
		}
		if _, ok := labels["cloud.google.com/gke-nodepool"]; ok {
			return ProviderGCPGKE
		}
	}
	return ProviderUnknown
}

func isAWS(provider string) bool   { return strings.HasPrefix(provider, "AWS") }
func isAzure(provider string) bool { return strings.HasPrefix(provider, "Azure") }

// pricingSupported reports whether a provider has a price source. Unknown
// clusters with AWS-shaped regions (kubeadm on EC2 without a cloud
// controller) still try the AWS price list.
func pricingSupported(provider string, regions []string) bool {
	if isAWS(provider) || isAzure(provider) {
		return true
	}
	if provider != ProviderUnknown {
		return false
	}
	for _, r := range regions {
		if awsRegionPattern.MatchString(r) {
			return true
		}
	}
	return false
}

func controlPlaneHourly(provider string) float64 {
	if provider == ProviderAWSEKS {
		return eksControlPlaneHourly
	}
	return 0
}

func instanceType(labels map[string]string) string {
	if it := labels["node.kubernetes.io/instance-type"]; it != "" {
		return it
	}
	return labels["beta.kubernetes.io/instance-type"]
}

func nodeRegion(labels map[string]string) string {
	if r := labels["topology.kubernetes.io/region"]; r != "" {
		return r
	}
	return labels["failure-domain.beta.kubernetes.io/region"]
}

func isSpot(labels map[string]string) bool {
	switch {
	case labels["karpenter.sh/capacity-type"] == "spot",
		labels["eks.amazonaws.com/capacityType"] == "SPOT",
		labels["node.kubernetes.io/lifecycle"] == "spot",
		labels["kubernetes.azure.com/scalesetpriority"] == "spot",
		labels["cloud.google.com/gke-spot"] == "true",
		labels["cloud.google.com/gke-preemptible"] == "true":
		return true
	}
	return false
}

// dominantRegion is the most common node region, for the summary header.
func dominantRegion(nodes []v1.Node) string {
	counts := map[string]int{}
	for i := range nodes {
		if r := nodeRegion(nodes[i].Labels); r != "" {
			counts[r]++
		}
	}
	best, bestN := "", 0
	for r, n := range counts {
		if n > bestN || (n == bestN && r < best) {
			best, bestN = r, n
		}
	}
	return best
}

func uniqueRegions(nodes []v1.Node) []string {
	set := map[string]struct{}{}
	for i := range nodes {
		if r := nodeRegion(nodes[i].Labels); r != "" {
			set[r] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
