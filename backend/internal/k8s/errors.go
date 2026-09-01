package k8s

import "strings"

// ClassifyClusterError maps a raw error string to a stable error code and a
// user-facing message. Returns ok=false when no pattern matches; callers can
// then fall back to a generic "cluster_error" with the original text.
//
// The matching order is significant: more specific patterns come first to
// avoid false positives (e.g. EKS error strings contain "?timeout=5s" in the
// URL, which must not be classified as a network timeout).
func ClassifyClusterError(errMsg string) (code, message string, ok bool) {
	if errMsg == "" {
		return "", "", false
	}
	lower := strings.ToLower(errMsg)

	if strings.Contains(lower, "sso session") && (strings.Contains(lower, "expired") || strings.Contains(lower, "invalid")) {
		return "aws_sso_expired", "Your AWS SSO session has expired. Please run 'aws sso login' to refresh.", true
	}
	if strings.Contains(lower, "security token") && strings.Contains(lower, "expired") {
		return "aws_token_expired", "Your AWS security token has expired. Please refresh your credentials.", true
	}
	if strings.Contains(lower, "aadsts") || strings.Contains(lower, "azure active directory") {
		return "azure_auth_expired", "Your Azure AD token has expired. Please run 'az login' to refresh.", true
	}
	if strings.Contains(lower, "kubelogin") {
		return "azure_kubelogin", "Azure authentication failed. Please run 'kubelogin convert-kubeconfig' or 'az login'.", true
	}
	if strings.Contains(lower, "gcloud") || (strings.Contains(lower, "google") && strings.Contains(lower, "credential")) {
		return "gcp_auth_expired", "Your GCP credentials have expired. Please run 'gcloud auth login'.", true
	}
	if strings.Contains(lower, "exec: executable") && strings.Contains(lower, "failed") {
		return "exec_failed", "Failed to execute authentication command. Please check your kubeconfig credentials.", true
	}
	if strings.Contains(lower, "getting credentials") {
		return "exec_failed", "Failed to obtain cluster credentials. Please check your kubeconfig.", true
	}
	if strings.Contains(lower, "the server has asked for the client to provide credentials") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, " 401 ") || strings.HasSuffix(lower, " 401") || strings.Contains(lower, "401 unauthorized") {
		return "unauthorized", "Authentication failed. Your credentials may have expired or be invalid for this cluster.", true
	}
	if strings.Contains(lower, "forbidden") || strings.Contains(lower, " 403 ") || strings.HasSuffix(lower, " 403") || strings.Contains(lower, "403 forbidden") {
		return "forbidden", "Access denied. You may not have permission to access this cluster.", true
	}
	if strings.Contains(lower, "token has expired") || strings.Contains(lower, "token expired") {
		return "token_expired", "Your authentication token has expired. Please refresh your credentials.", true
	}
	if strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "context deadline exceeded") {
		return "timeout", "Connection to the cluster timed out. Please check your network connection.", true
	}
	if strings.Contains(lower, "connection refused") {
		return "connection_failed", "Unable to connect to the cluster. Please check if the cluster is reachable.", true
	}
	if strings.Contains(lower, "no such host") || strings.Contains(lower, "dial tcp") {
		return "connection_failed", "Unable to reach the cluster. Please check your network connection or VPN.", true
	}
	if strings.Contains(lower, "certificate") && (strings.Contains(lower, "expired") || strings.Contains(lower, "invalid")) {
		return "cert_error", "Certificate error. The cluster certificate may be expired or invalid.", true
	}
	if strings.Contains(lower, "x509") {
		return "cert_error", "TLS/Certificate error. Please check your cluster's certificate configuration.", true
	}
	return "", "", false
}
