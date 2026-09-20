package k8s

import (
	"errors"
	"strings"
)

// ErrResourceGone is returned by UpdateResource when the object being edited
// no longer exists in the cluster. Callers must surface it, never recreate the
// object: the caller believed it was editing something live, and resurrecting
// a resource someone else deleted can restore obsolete or unsafe configuration.
var ErrResourceGone = errors.New("resource no longer exists")

// IsResourceGone reports whether err wraps ErrResourceGone.
func IsResourceGone(err error) bool {
	return errors.Is(err, ErrResourceGone)
}

// IsAuthErrorCode reports whether a classified cluster error code means the
// user's cloud credentials need attention (as opposed to network/cert issues).
func IsAuthErrorCode(code string) bool {
	switch code {
	case "aws_sso_expired", "aws_token_expired", "aws_credentials_missing",
		"azure_auth_expired", "azure_kubelogin", "gcp_auth_expired", "gcp_plugin_missing",
		"unauthorized", "token_expired", "exec_missing":
		return true
	}
	return false
}

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

	// Missing credential helper binaries. client-go reports these as
	// `exec: executable aws not found` (plus a "credential plugin that is not
	// installed" hint) or `exec: "aws": executable file not found in $PATH`.
	if strings.Contains(lower, "executable file not found") ||
		strings.Contains(lower, "credential plugin that is not installed") ||
		(strings.Contains(lower, "exec: executable") && strings.Contains(lower, "not found")) ||
		(strings.Contains(lower, "no such file or directory") && strings.Contains(lower, "exec")) {
		switch {
		case strings.Contains(lower, `"aws"`) || strings.Contains(lower, "executable aws ") || strings.Contains(lower, "aws-iam-authenticator"):
			return "exec_missing", "The AWS CLI is not installed or not on PATH, so this cluster's credentials cannot be fetched.", true
		case strings.Contains(lower, "gke-gcloud-auth-plugin"):
			return "gcp_plugin_missing", "gke-gcloud-auth-plugin is not installed. Install it with `gcloud components install gke-gcloud-auth-plugin`.", true
		case strings.Contains(lower, "gcloud"):
			return "exec_missing", "The Google Cloud CLI is not installed or not on PATH.", true
		case strings.Contains(lower, "kubelogin"):
			return "exec_missing", "kubelogin is not installed. Install it with `az aks install-cli`.", true
		case strings.Contains(lower, `"az"`) || strings.Contains(lower, "executable az "):
			return "exec_missing", "The Azure CLI is not installed or not on PATH.", true
		}
		return "exec_missing", "A credential helper referenced by this kubeconfig is not installed.", true
	}

	// AWS IAM Identity Center.
	if (strings.Contains(lower, "sso session") && (strings.Contains(lower, "expired") || strings.Contains(lower, "invalid"))) ||
		strings.Contains(lower, "error loading sso token") ||
		(strings.Contains(lower, "token for") && strings.Contains(lower, "does not exist")) ||
		strings.Contains(lower, "sso token") && strings.Contains(lower, "not found") ||
		strings.Contains(lower, "invalid_grant") && strings.Contains(lower, "sso") ||
		strings.Contains(lower, "unauthorizedexception") && strings.Contains(lower, "sso") {
		return "aws_sso_expired", "Your AWS SSO session has expired. Sign in again to reconnect.", true
	}
	if strings.Contains(lower, "security token") && (strings.Contains(lower, "expired") || strings.Contains(lower, "invalid")) ||
		strings.Contains(lower, "expiredtoken") {
		return "aws_token_expired", "Your AWS credentials have expired. Refresh them in the tool that issued them, or sign in again.", true
	}
	if strings.Contains(lower, "unable to locate credentials") ||
		strings.Contains(lower, "nocredentialproviders") ||
		strings.Contains(lower, "no valid credential sources") ||
		strings.Contains(lower, "failed to refresh cached credentials") ||
		(strings.Contains(lower, "profile") && strings.Contains(lower, "could not be found")) ||
		strings.Contains(lower, "the config profile") && strings.Contains(lower, "could not be found") {
		return "aws_credentials_missing", "No valid AWS credentials were found for this cluster's profile. Start its session in your credential tool or sign in again.", true
	}

	// Azure.
	if strings.Contains(lower, "aadsts") || strings.Contains(lower, "azure active directory") || strings.Contains(lower, "re-authenticate") && strings.Contains(lower, "azure") {
		return "azure_auth_expired", "Your Azure sign-in has expired. Sign in with the Azure CLI again.", true
	}
	if strings.Contains(lower, "please run 'az login'") || strings.Contains(lower, "please run \"az login\"") || strings.Contains(lower, "az login") && strings.Contains(lower, "setup account") {
		return "azure_auth_expired", "You are not signed in to Azure. Sign in with the Azure CLI.", true
	}
	if strings.Contains(lower, "kubelogin") {
		return "azure_kubelogin", "Azure authentication failed. Sign in with the Azure CLI, or run `kubelogin convert-kubeconfig -l azurecli`.", true
	}

	// Google Cloud.
	if strings.Contains(lower, "reauthentication required") || strings.Contains(lower, "reauthentication failed") ||
		(strings.Contains(lower, "invalid_grant") && (strings.Contains(lower, "google") || strings.Contains(lower, "gcloud") || strings.Contains(lower, "gke"))) ||
		strings.Contains(lower, "token has been expired or revoked") {
		return "gcp_auth_expired", "Your Google Cloud sign-in has expired. Sign in with gcloud again.", true
	}
	if strings.Contains(lower, "gke-gcloud-auth-plugin") && (strings.Contains(lower, "not found") || strings.Contains(lower, "no such file")) {
		return "gcp_plugin_missing", "gke-gcloud-auth-plugin is not installed. Install it with `gcloud components install gke-gcloud-auth-plugin`.", true
	}
	if strings.Contains(lower, "gcloud") || (strings.Contains(lower, "google") && strings.Contains(lower, "credential")) {
		return "gcp_auth_expired", "Your Google Cloud credentials are unavailable. Sign in with gcloud.", true
	}

	// Generic exec plugin failures.
	if strings.Contains(lower, "exec: executable") && strings.Contains(lower, "failed") {
		return "exec_failed", "Failed to execute the credential helper. Check your kubeconfig credentials.", true
	}
	if strings.Contains(lower, "getting credentials") {
		return "exec_failed", "Failed to obtain cluster credentials. Check your kubeconfig.", true
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
