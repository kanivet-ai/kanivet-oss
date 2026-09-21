package cloud

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/ini.v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// awsProfileKind classifies how a named AWS profile obtains credentials.
type awsProfileKind struct {
	Exists            bool
	SSOStartURL       string
	SSORegion         string
	SSOSessionName    string
	StaticCredentials bool
	CredentialProcess bool
	SourceProfile     string
	RoleArn           string
}

func inspectAWSProfile(name string, cfg, creds *ini.File) awsProfileKind {
	kind := awsProfileKind{}
	sectionName := "profile " + name
	if name == "default" {
		sectionName = "default"
	}
	var section *ini.Section
	if cfg != nil && cfg.HasSection(sectionName) {
		section = cfg.Section(sectionName)
	} else if cfg != nil && name != "default" && cfg.HasSection(name) {
		section = cfg.Section(name)
	}
	if section != nil {
		kind.Exists = true
		if sess := section.Key("sso_session").String(); sess != "" {
			kind.SSOSessionName = sess
			if cfg.HasSection("sso-session " + sess) {
				s := cfg.Section("sso-session " + sess)
				kind.SSOStartURL = s.Key("sso_start_url").String()
				kind.SSORegion = s.Key("sso_region").String()
			}
		} else if startURL := section.Key("sso_start_url").String(); startURL != "" {
			kind.SSOStartURL = startURL
			kind.SSORegion = section.Key("sso_region").String()
		}
		kind.CredentialProcess = section.HasKey("credential_process")
		kind.SourceProfile = section.Key("source_profile").String()
		kind.RoleArn = section.Key("role_arn").String()
		if section.HasKey("aws_access_key_id") {
			kind.StaticCredentials = true
		}
	}
	if creds != nil && creds.HasSection(name) {
		kind.Exists = true
		if creds.Section(name).HasKey("aws_access_key_id") {
			kind.StaticCredentials = true
		}
		if creds.Section(name).HasKey("credential_process") {
			kind.CredentialProcess = true
		}
	}
	return kind
}

func execFlagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"=")
		}
	}
	return ""
}

func execEnvValue(env []clientcmdapi.ExecEnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

// describeClusterAuth inspects the kubeconfig context and explains how it
// authenticates, resolving AWS profiles down to their SSO portal so the UI can
// offer the exact sign-in that will fix a failing cluster.
func (p *AWSProvider) describeClusterAuth(cluster, kubeconfigPath string) (*ClusterAuthInfo, error) {
	return p.describeBoundClusterAuth(cluster, kubeconfigPath, nil)
}

// describeBoundClusterAuth describes a context as Kanivet connects to it: with
// a binding, the exec plugin runs under the binding's profile.
func (p *AWSProvider) describeBoundClusterAuth(cluster, kubeconfigPath string, binding *ClusterSSOBinding) (*ClusterAuthInfo, error) {
	info := &ClusterAuthInfo{Cluster: cluster, Provider: "other", Method: "none", KubeconfigPath: kubeconfigPath, SSOBinding: binding}
	if kubeconfigPath == "" {
		return info, fmt.Errorf("context %s not found in any kubeconfig", cluster)
	}
	kubeconfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return info, err
	}
	ctx, ok := kubeconfig.Contexts[cluster]
	if !ok {
		return info, fmt.Errorf("context %s not found in %s", cluster, kubeconfigPath)
	}
	if clusterCfg, ok := kubeconfig.Clusters[ctx.Cluster]; ok {
		server := strings.ToLower(clusterCfg.Server)
		switch {
		case strings.Contains(server, ".eks.amazonaws.com"):
			info.Provider = "aws"
		case strings.Contains(server, ".azmk8s.io"):
			info.Provider = "azure"
		case strings.Contains(server, ".gke.io") || strings.Contains(server, "container.googleapis.com"):
			info.Provider = "gcp"
		}
	}
	if info.Provider == "other" {
		switch {
		case strings.HasPrefix(cluster, "arn:aws:eks:"):
			info.Provider = "aws"
		case strings.HasPrefix(cluster, "gke_"):
			info.Provider = "gcp"
		}
	}

	if info.Provider == "aws" {
		info.AccountID = eksAccountFromContext(cluster)
		if info.AccountID == "" {
			info.AccountID = eksAccountFromContext(ctx.Cluster)
		}
	}

	authInfo := kubeconfig.AuthInfos[ctx.AuthInfo]
	if authInfo == nil {
		return info, nil
	}
	switch {
	case authInfo.Exec != nil:
		p.describeExecAuth(info, authInfo.Exec)
	case authInfo.Token != "" || authInfo.TokenFile != "":
		info.Method = "token"
	case authInfo.ClientCertificate != "" || len(authInfo.ClientCertificateData) > 0:
		info.Method = "client-cert"
	case authInfo.Username != "":
		info.Method = "basic"
	case authInfo.AuthProvider != nil:
		info.Method = "auth-provider:" + authInfo.AuthProvider.Name
		if authInfo.AuthProvider.Name == "azure" {
			info.Provider = "azure"
			info.SignIn = "azure"
			info.Hint = "This kubeconfig uses the removed 'azure' auth provider. Run `kubelogin convert-kubeconfig -l azurecli` or re-import the cluster from Kanivet."
		}
		if authInfo.AuthProvider.Name == "gcp" {
			info.Provider = "gcp"
			info.SignIn = "gcp"
			info.Hint = "This kubeconfig uses the removed 'gcp' auth provider. Re-import the cluster or run `gcloud container clusters get-credentials`."
		}
	}
	return info, nil
}

func (p *AWSProvider) describeExecAuth(info *ClusterAuthInfo, execCfg *clientcmdapi.ExecConfig) {
	command := filepath.Base(execCfg.Command)
	info.Command = command
	_, info.CommandInstalled = lookPath(command)
	if !info.CommandInstalled {
		if hint := cliInstallHint(command); hint != "" {
			info.InstallHint = hint
		} else if execCfg.InstallHint != "" {
			info.InstallHint = execCfg.InstallHint
		}
	}
	switch command {
	case "aws", "aws-iam-authenticator":
		info.Provider = "aws"
		p.describeAWSExec(info, execCfg)
	case "gke-gcloud-auth-plugin", "gcloud":
		info.Provider = "gcp"
		info.Method = "gcp-plugin"
		info.SignIn = "gcp"
		if _, ok := lookPath("gcloud"); !ok {
			info.InstallHint = cliInstallHint("gcloud")
		}
	case "kubelogin":
		info.Provider = "azure"
		info.Method = "azure-kubelogin"
		info.SignIn = "azure"
		login := execFlagValue(execCfg.Args, "--login")
		if login == "" {
			login = execFlagValue(execCfg.Args, "-l")
		}
		if login != "" && login != "azurecli" && login != "azd" && login != "msi" && login != "workloadidentity" && login != "spn" {
			info.Hint = fmt.Sprintf("kubelogin uses the '%s' login mode, which needs an interactive terminal. Run `kubelogin convert-kubeconfig -l azurecli` on this kubeconfig so it reuses your `az login`.", login)
		}
	case "az":
		info.Provider = "azure"
		info.Method = "azure-cli"
		info.SignIn = "azure"
	default:
		info.Method = "exec"
	}
}

func (p *AWSProvider) describeAWSExec(info *ClusterAuthInfo, execCfg *clientcmdapi.ExecConfig) {
	profile := ""
	if info.SSOBinding != nil {
		profile = info.SSOBinding.Profile
	}
	if profile == "" {
		profile = execEnvValue(execCfg.Env, "AWS_PROFILE")
	}
	if profile == "" {
		profile = execFlagValue(execCfg.Args, "--profile")
	}
	if profile == "" {
		profile = execEnvValue(execCfg.Env, "AWS_DEFAULT_PROFILE")
	}
	if info.SSOBinding == nil && execEnvValue(execCfg.Env, "AWS_ACCESS_KEY_ID") != "" {
		info.Method = "aws-static"
		info.ExternalTool = true
		info.Hint = "This context carries static AWS keys in its exec environment."
		return
	}
	configPath, _ := awsConfigPath()
	credPath, _ := awsCredentialsPath()
	cfg, _ := loadINIFile(configPath)
	creds, _ := loadINIFile(credPath)

	if profile == "" {
		if envProfile := os.Getenv("AWS_PROFILE"); envProfile != "" {
			profile = envProfile
		} else {
			profile = "default"
		}
	}
	info.Profile = profile

	kind := inspectAWSProfile(profile, cfg, creds)
	// Follow source_profile chains (role assumption) to the credential source.
	for depth := 0; depth < 5 && kind.SSOStartURL == "" && kind.SourceProfile != "" && kind.SourceProfile != profile; depth++ {
		next := inspectAWSProfile(kind.SourceProfile, cfg, creds)
		if !next.Exists {
			break
		}
		roleArn := kind.RoleArn
		kind = next
		kind.RoleArn = roleArn
	}

	switch {
	case kind.SSOStartURL != "":
		info.Method = "aws-sso"
		info.SignIn = "aws-sso"
		info.SSOStartURL = normalizeStartURL(kind.SSOStartURL)
		info.SSORegion = kind.SSORegion
		info.SSOSessionName = kind.SSOSessionName
		info.SSOState = p.sessionStatus(kind.SSOStartURL, kind.SSORegion, time.Now()).State
	case kind.CredentialProcess:
		info.Method = "aws-credential-process"
		info.ExternalTool = true
		info.Hint = fmt.Sprintf("Profile %s gets its credentials from an external credential_process (for example aws-vault or granted). Refresh that tool's session, then retry.", profile)
	case kind.StaticCredentials:
		info.Method = "aws-static"
		info.ExternalTool = true
		info.Hint = fmt.Sprintf("Profile %s holds credentials written by another tool (Leapp, aws-vault, or `aws configure`). Start or refresh that session, then retry.", profile)
	case !kind.Exists && profile != "default":
		info.Method = "aws-profile-missing"
		info.Hint = fmt.Sprintf("Profile %s does not exist in ~/.aws/config or ~/.aws/credentials. Re-import the cluster or create the profile.", profile)
	default:
		info.Method = "aws-default-chain"
		info.ExternalTool = true
		info.Hint = "This context relies on the default AWS credential chain (environment variables, the default profile, or an instance role)."
		if account := eksAccountFromContext(info.Cluster); account != "" && cfg != nil {
			_, ssoProfiles := parseAWSSSOConfig(cfg)
			for _, prof := range ssoProfiles {
				if prof.AccountID == account && !strings.HasPrefix(prof.Name, "kanivet-") {
					info.MatchingProfiles = append(info.MatchingProfiles, prof.Name)
				}
			}
			if len(info.MatchingProfiles) > 0 {
				info.Hint += fmt.Sprintf(" Profiles for account %s exist in ~/.aws/config (%s); re-import the cluster from Kanivet or add `env: [{name: AWS_PROFILE, value: <profile>}]` to its exec block so it uses one of them.", account, strings.Join(info.MatchingProfiles, ", "))
			}
		}
	}
}

// eksAccountFromContext extracts the account ID from an EKS cluster ARN used
// as a context name, or "" when the name is not an ARN.
func eksAccountFromContext(name string) string {
	if !strings.HasPrefix(name, "arn:aws:eks:") {
		return ""
	}
	parts := strings.Split(name, ":")
	if len(parts) < 6 {
		return ""
	}
	return parts[4]
}
