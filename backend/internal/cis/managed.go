package cis

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kanivet/backend/internal/health"
)

// awsAuthCheck reviews EKS access: the legacy aws-auth ConfigMap versus
// access entries, and mappings straight into system:masters.
func awsAuthCheck(in *Inputs) Outcome {
	if in.AWSAuthErr != "" {
		return notVisible("kube-system/aws-auth could not be read: " + in.AWSAuthErr)
	}
	ref := health.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "kube-system", Name: "aws-auth"}
	if !in.AWSAuthFound {
		return Outcome{Status: StatusPass,
			Message:     "There is no aws-auth ConfigMap, so access is managed with EKS access entries.",
			ManualCheck: "Review the cluster's access entries and their access policies in the EKS console or with aws eks list-access-entries (Kanivet does not call the EKS API for this)."}
	}
	var masters []string
	for _, key := range []string{"mapRoles", "mapUsers"} {
		entries := splitEntries(in.AWSAuth[key])
		for _, e := range entries {
			if strings.Contains(e, "system:masters") {
				masters = append(masters, key+": "+firstARN(e))
			}
		}
	}
	o := Outcome{Resources: []health.ResourceRef{ref}}
	for _, m := range masters {
		o.Values = append(o.Values, Value{Name: "system:masters mapping", Value: m})
	}
	if len(masters) > 0 {
		o.Status = StatusFail
		o.Message = fmt.Sprintf("aws-auth maps %d IAM identit(ies) to system:masters, which RBAC cannot restrict.", len(masters))
		return o
	}
	o.Status = StatusManual
	o.Message = "The legacy aws-auth ConfigMap is in use; no entry maps to system:masters."
	o.ManualCheck = "Plan the move to EKS access entries (authentication mode API) and review each mapped role's groups."
	return o
}

// splitEntries cuts a mapRoles/mapUsers YAML list into its items without a
// YAML parser: each item starts with "- ".
func splitEntries(s string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") && !strings.HasPrefix(line, "    ") && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

func firstARN(entry string) string {
	for _, line := range strings.Split(entry, "\n") {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		for _, k := range []string{"rolearn:", "userarn:"} {
			if v, ok := strings.CutPrefix(t, k); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return "entry"
}

func privateNodes(in *Inputs) Outcome {
	if in.Snap == nil || len(in.Snap.Nodes) == 0 {
		return notVisible("Nodes could not be read.")
	}
	var refs []health.ResourceRef
	for _, n := range in.Snap.Nodes {
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeExternalIP && a.Address != "" {
				refs = append(refs, health.ResourceRef{APIVersion: "v1", Kind: "Node", Name: n.Name})
				break
			}
		}
	}
	if len(refs) == 0 {
		return pass(fmt.Sprintf("None of %d nodes reports an external IP.", len(in.Snap.Nodes)))
	}
	return Outcome{Status: StatusFail, Resources: refs, Message: fmt.Sprintf("%d of %d nodes have a public (external) IP.", len(refs), len(in.Snap.Nodes))}
}

func cosNodes(in *Inputs) Outcome {
	if in.Snap == nil || len(in.Snap.Nodes) == 0 {
		return notVisible("Nodes could not be read.")
	}
	var refs []health.ResourceRef
	for _, n := range in.Snap.Nodes {
		if !strings.Contains(n.Status.NodeInfo.OSImage, "Container-Optimized OS") {
			refs = append(refs, health.ResourceRef{APIVersion: "v1", Kind: "Node", Name: n.Name})
		}
	}
	if len(refs) == 0 {
		return pass("Every node runs Container-Optimized OS.")
	}
	return Outcome{Status: StatusFail, Resources: refs, Message: fmt.Sprintf("%d node(s) run another OS image.", len(refs))}
}

func manualCheck(desc, what string) check { return check{desc: desc, manual: what} }

func init() {
	reg("eks.access", check{
		desc:        "Cluster access is managed with EKS access entries, and no IAM identity maps to system:masters.",
		remediation: "Switch the cluster's authentication mode to API (or API_AND_CONFIG_MAP while migrating), create access entries, then remove aws-auth.",
		snippet:     "aws eks update-cluster-config --name <cluster> --access-config authenticationMode=API_AND_CONFIG_MAP",
		eval:        awsAuthCheck,
	})
	reg("eks.audit-logs", manualCheck("The EKS control plane sends audit logs to CloudWatch.",
		"Control-plane logging is an EKS API setting, not visible from Kubernetes. Run aws eks describe-cluster --name <cluster> --query cluster.logging and check that audit (and authenticator) are enabled."))
	reg("eks.ecr-scan", manualCheck("ECR repositories scan images on push.", "Check the ECR registry scanning configuration (basic or enhanced) for the repositories this cluster pulls from."))
	reg("eks.ecr-access", manualCheck("Few users can push to ECR.", "Review IAM policies granting ecr:PutImage and related actions."))
	reg("eks.ecr-readonly", manualCheck("Node roles have read-only ECR access.", "Check the node IAM role uses AmazonEC2ContainerRegistryReadOnly (or tighter), not write access."))
	reg("eks.kms", manualCheck("Kubernetes Secrets are envelope-encrypted with a KMS key.", "Run aws eks describe-cluster --query cluster.encryptionConfig. Clusters on Kubernetes 1.28+ also get default KMS v2 encryption."))
	reg("eks.endpoint", manualCheck("The public endpoint, if on, is limited to known CIDRs.", "Run aws eks describe-cluster --query cluster.resourcesVpcConfig and check publicAccessCidrs is not 0.0.0.0/0."))
	reg("eks.private-endpoint", manualCheck("The private endpoint is on and public access is off.", "Check endpointPrivateAccess=true and endpointPublicAccess=false in the cluster's VPC config."))
	reg("eks.private-nodes", check{desc: "Worker nodes have no public IPs.", remediation: "Launch node groups in private subnets.", eval: privateNodes})
	reg("eks.lb-tls", manualCheck("Load balancers terminate TLS.", "Check Services of type LoadBalancer and Ingresses use TLS listeners (service.beta.kubernetes.io/aws-load-balancer-ssl-cert or ALB certificate-arn)."))
	reg("eks.iam-users", manualCheck("People reach the cluster through IAM roles or an OIDC identity provider.", "Check no long-lived IAM users or static credentials are mapped for human access."))

	reg("gke.audit-logs", manualCheck("Cloud Audit Logs capture admin and data access for the cluster.", "Check the project's audit log configuration for the Kubernetes Engine API."))
	reg("gke.image-scanning", manualCheck("Artifact Registry scans images.", "Check Artifact Analysis vulnerability scanning is on for the registries in use."))
	reg("gke.node-sa", manualCheck("Node pools use a dedicated, least-privilege service account.", "Run gcloud container node-pools describe and check serviceAccount is not the default compute account."))
	reg("gke.workload-identity", manualCheck("Workload Identity Federation is on.", "Check the cluster's workloadPool is set and node pools use GKE_METADATA."))
	reg("gke.kms", manualCheck("Application-layer secrets encryption uses Cloud KMS.", "Check databaseEncryption.state is ENCRYPTED in the cluster description."))
	reg("gke.cos", check{desc: "Nodes run Container-Optimized OS.", remediation: "Use the COS_CONTAINERD image type for node pools.", eval: cosNodes})
	reg("gke.shielded-nodes", manualCheck("Shielded GKE nodes with secure boot.", "Check shieldedNodes.enabled and node pool shieldedInstanceConfig."))
	reg("gke.vpc-native", manualCheck("The cluster uses alias IPs.", "Check ipAllocationPolicy.useIpAliases is true."))
	reg("gke.private-endpoint", manualCheck("The control plane endpoint is private or restricted.", "Check privateClusterConfig and masterAuthorizedNetworksConfig."))
	reg("gke.network-policy", check{desc: "Network policy enforcement is enabled (Dataplane V2 or Calico).", remediation: "Enable Dataplane V2 or network policy enforcement on the cluster.", eval: cniCheck})
	reg("gke.basic-auth", manualCheck("Legacy basic and client-certificate authentication are off.", "Check masterAuth has no username or issued client certificate."))
	reg("gke.binauthz", manualCheck("Binary Authorization enforces image policy.", "Check binaryAuthorization.evaluationMode is set."))

	reg("aks.audit-logs", manualCheck("Diagnostic settings send kube-audit logs to a workspace.", "Check the cluster's diagnostic settings include kube-audit or kube-audit-admin."))
	reg("aks.image-scanning", manualCheck("Images are scanned (Defender for Containers).", "Check Microsoft Defender for Containers is enabled for the registry and cluster."))
	reg("aks.kms", manualCheck("Secrets are encrypted with a Key Vault key.", "Check securityProfile.azureKeyVaultKms is enabled."))
	reg("aks.endpoint", manualCheck("API server authorized IP ranges are set.", "Check apiServerAccessProfile.authorizedIpRanges."))
	reg("aks.private-cluster", manualCheck("The cluster is private.", "Check apiServerAccessProfile.enablePrivateCluster."))
	reg("aks.entra", manualCheck("Users authenticate with Microsoft Entra ID.", "Check aadProfile is set and local accounts are disabled (disableLocalAccounts)."))
}
