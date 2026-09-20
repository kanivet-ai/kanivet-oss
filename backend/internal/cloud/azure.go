package cloud

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// AzureProvider discovers AKS clusters with the identity the user's `az` CLI
// holds, so a terminal `az login` is all that is ever needed.
type AzureProvider struct {
	cred          azcore.TokenCredential
	mu            sync.RWMutex
	cli           *cliLoginManager
	onAuthChanged func()
}

func NewAzureProvider() *AzureProvider {
	p := &AzureProvider{cli: newCLILoginManager()}
	p.cli.onDone = func(job *CLILoginJob) {
		p.mu.Lock()
		p.cred = nil
		p.mu.Unlock()
		if p.onAuthChanged != nil {
			p.onAuthChanged()
		}
	}
	return p
}

func (p *AzureProvider) SetOnAuthChanged(fn func()) { p.onAuthChanged = fn }

func (p *AzureProvider) ensureCredential() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cred != nil {
		return nil
	}
	// Prefer the Azure CLI credential: it is what the user signed in with and
	// it never stalls on managed-identity probes the way the default chain does.
	if _, ok := lookPath("az"); ok {
		cred, err := azidentity.NewAzureCLICredential(nil)
		if err == nil {
			p.cred = cred
			return nil
		}
		log.Printf("[Azure] CLI credential unavailable, falling back to default chain: %v", err)
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}
	p.cred = cred
	return nil
}

func (p *AzureProvider) ListSubscriptions(ctx context.Context) ([]AzureSubscription, error) {
	if err := p.ensureCredential(); err != nil {
		return nil, err
	}

	client, err := armsubscriptions.NewClient(p.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
	}

	var subs []AzureSubscription
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subscriptions: %s", humanizeAzureError(err.Error()))
		}
		for _, sub := range page.Value {
			s := AzureSubscription{}
			if sub.SubscriptionID != nil {
				s.ID = *sub.SubscriptionID
			}
			if sub.DisplayName != nil {
				s.Name = *sub.DisplayName
			}
			if sub.TenantID != nil {
				s.TenantID = *sub.TenantID
			}
			if sub.State != nil {
				s.State = string(*sub.State)
			}
			subs = append(subs, s)
		}
	}
	return subs, nil
}

func humanizeAzureError(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "aadsts700082"), strings.Contains(lower, "aadsts70043"), strings.Contains(lower, "refresh token has expired"), strings.Contains(lower, "re-authenticate"), strings.Contains(lower, "reauthenticate"):
		return "your Azure sign-in has expired; run az login or sign in from Kanivet"
	case strings.Contains(lower, "please run 'az login'"), strings.Contains(lower, "az login"), strings.Contains(lower, "no subscription found"), strings.Contains(lower, "not logged in"):
		return "not signed in to Azure; run az login or sign in from Kanivet"
	case strings.Contains(lower, "executable file not found"), strings.Contains(lower, "az: command not found"):
		return "Azure CLI (az) is not installed"
	}
	return msg
}

type azAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
	User     struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"user"`
}

// Status reports the signed-in Azure account and whether its token still works.
func (p *AzureProvider) Status(ctx context.Context) ProviderAuthSummary {
	summary := ProviderAuthSummary{
		Provider:   ProviderAzure,
		CLIName:    "az",
		PluginName: "kubelogin",
		CheckedAt:  time.Now().UnixMilli(),
	}
	_, summary.CLIInstalled = lookPath("az")
	_, summary.PluginInstalled = lookPath("kubelogin")
	if !summary.CLIInstalled {
		summary.CLIInstallHint = cliInstallHint("az")
	}
	if !summary.PluginInstalled {
		summary.PluginInstallHint = cliInstallHint("kubelogin")
	}
	if job, ok := p.cli.running("azure"); ok {
		summary.Login = job
	}
	if !summary.CLIInstalled {
		summary.State = "unavailable"
		summary.Detail = "Azure CLI not found"
		return summary
	}

	showCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := runCLI(showCtx, "az", []string{"account", "show", "-o", "json", "--only-show-errors"}, nil)
	if err != nil {
		summary.State = "signed_out"
		summary.Detail = humanizeAzureError(out)
		return summary
	}
	var acc azAccount
	if start := strings.Index(out, "{"); start >= 0 {
		_ = jsonv2.Unmarshal([]byte(out[start:]), &acc)
	}
	summary.Identity = acc.User.Name
	if acc.Name != "" {
		summary.Detail = acc.Name
	}

	tokenCtx, cancelToken := context.WithTimeout(ctx, 25*time.Second)
	defer cancelToken()
	tokOut, err := runCLI(tokenCtx, "az", []string{"account", "get-access-token", "--query", "expiresOn", "-o", "tsv", "--only-show-errors"}, nil)
	if err != nil {
		summary.State = "expired"
		summary.Error = humanizeAzureError(tokOut)
		return summary
	}
	if expires := lastNonEmptyLine(tokOut); expires != "" {
		for _, layout := range []string{"2006-01-02 15:04:05.000000", "2006-01-02 15:04:05", time.RFC3339} {
			if t, err := time.ParseInLocation(layout, expires, time.Local); err == nil {
				summary.ExpiresAt = t.UnixMilli()
				break
			}
		}
	}
	summary.State = "active"
	summary.SignedIn = true
	return summary
}

func (p *AzureProvider) IsAuthenticated(ctx context.Context) bool {
	return p.Status(ctx).SignedIn
}

// Login runs `az login` in the background; az opens the browser (or prints a
// device code, which the job exposes) and the UI polls the job.
func (p *AzureProvider) Login(ctx context.Context) (*CLILoginJob, error) {
	return p.cli.start("azure", ProviderAzure, "Azure", "az", []string{"login", "--only-show-errors"}, cliEnv())
}

func (p *AzureProvider) LoginJob(id string) (*CLILoginJob, bool) { return p.cli.get(id) }
func (p *AzureProvider) CancelLogin(id string) bool              { return p.cli.cancel(id) }

func (p *AzureProvider) DiscoverClusters(ctx context.Context, subscriptionID string) ([]DiscoveredCluster, error) {
	if err := p.ensureCredential(); err != nil {
		return nil, err
	}

	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, p.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS client: %w", err)
	}

	var clusters []DiscoveredCluster
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if len(clusters) == 0 {
				return nil, fmt.Errorf("failed to list AKS clusters: %s", humanizeAzureError(err.Error()))
			}
			log.Printf("Error listing AKS clusters: %v", err)
			break
		}
		for _, c := range page.Value {
			if c.ID == nil || c.Name == nil {
				continue
			}
			rg := extractResourceGroup(*c.ID)
			cluster := DiscoveredCluster{
				ID:            *c.ID,
				Name:          *c.Name,
				Provider:      ProviderAzure,
				AccountID:     subscriptionID,
				ResourceGroup: rg,
				HasAccess:     true,
				AccessChecked: true,
			}
			if c.Location != nil {
				cluster.Region = *c.Location
			}
			if c.Properties != nil {
				if c.Properties.ProvisioningState != nil {
					cluster.Status = *c.Properties.ProvisioningState
				}
				if c.Properties.KubernetesVersion != nil {
					cluster.Version = *c.Properties.KubernetesVersion
				}
				if c.Properties.Fqdn != nil {
					cluster.Endpoint = fmt.Sprintf("https://%s", *c.Properties.Fqdn)
				}
			}
			if c.Tags != nil {
				cluster.Tags = make(map[string]string)
				for k, v := range c.Tags {
					if v != nil {
						cluster.Tags[k] = *v
					}
				}
			}
			clusters = append(clusters, cluster)
		}
	}
	return clusters, nil
}

func extractResourceGroup(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// ImportCluster fetches the cluster's kubeconfig through ARM and merges it
// into Kanivet's kubeconfig. Entra-ID clusters come back with a kubelogin exec
// that prompts for a device code on stdin; we rewrite it to `--login azurecli`
// so the token comes from the user's `az login` silently.
func (p *AzureProvider) ImportCluster(ctx context.Context, req ImportRequest) error {
	if err := p.ensureCredential(); err != nil {
		return err
	}

	client, err := armcontainerservice.NewManagedClustersClient(req.AccountID, p.cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create AKS client: %w", err)
	}

	_, kubeloginInstalled := lookPath("kubelogin")
	var kubeconfigs []*armcontainerservice.CredentialResult
	if kubeloginInstalled {
		if resp, err := client.ListClusterUserCredentials(ctx, req.ResourceGroup, req.Name, nil); err == nil {
			kubeconfigs = resp.Kubeconfigs
		}
	}
	if len(kubeconfigs) == 0 {
		if resp, err := client.ListClusterAdminCredentials(ctx, req.ResourceGroup, req.Name, nil); err == nil {
			kubeconfigs = resp.Kubeconfigs
		} else if resp, err2 := client.ListClusterUserCredentials(ctx, req.ResourceGroup, req.Name, nil); err2 == nil {
			kubeconfigs = resp.Kubeconfigs
		} else {
			return fmt.Errorf("failed to get cluster credentials: %s", humanizeAzureError(err.Error()))
		}
	}
	return p.mergeKubeconfig(kubeconfigs)
}

// convertKubeloginToAzureCLI rewrites a kubelogin exec block so it uses the
// Azure CLI login mode (what `kubelogin convert-kubeconfig -l azurecli` does).
func convertKubeloginToAzureCLI(authInfo *clientcmdapi.AuthInfo) bool {
	if authInfo == nil || authInfo.Exec == nil || filepath.Base(authInfo.Exec.Command) != "kubelogin" {
		return false
	}
	args := authInfo.Exec.Args
	keep := []string{"get-token"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "get-token":
			continue
		case "--environment", "-e", "--server-id", "--tenant-id", "-t":
			if i+1 < len(args) {
				keep = append(keep, args[i], args[i+1])
				i++
			}
		case "--login", "-l", "--client-id", "--client-secret", "--username", "--password", "--identity-resource-id", "--authority-host", "--federated-token-file", "--token-cache-dir":
			i++ // drop flag and its value
		case "--legacy", "--use-azurerm-env-vars", "--pop-enabled", "--disable-instance-discovery", "--disable-environment-override":
			// drop bare flags
		default:
			keep = append(keep, args[i])
		}
	}
	keep = append(keep, "--login", "azurecli")
	authInfo.Exec.Args = keep
	authInfo.Exec.Env = nil
	authInfo.Exec.InteractiveMode = clientcmdapi.NeverExecInteractiveMode
	if authInfo.Exec.InstallHint == "" {
		authInfo.Exec.InstallHint = cliInstallHint("kubelogin")
	}
	return true
}

func (p *AzureProvider) mergeKubeconfig(kubeconfigs []*armcontainerservice.CredentialResult) error {
	if len(kubeconfigs) == 0 {
		return fmt.Errorf("no kubeconfig returned")
	}

	newConfig, err := clientcmd.Load(kubeconfigs[0].Value)
	if err != nil {
		return fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	kubeconfigPath := KanivetKubeconfigPath()
	existingConfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		existingConfig = clientcmdapi.NewConfig()
	}

	for name, cluster := range newConfig.Clusters {
		existingConfig.Clusters[name] = cluster
	}
	for name, authInfo := range newConfig.AuthInfos {
		convertKubeloginToAzureCLI(authInfo)
		existingConfig.AuthInfos[name] = authInfo
	}
	for name, context := range newConfig.Contexts {
		existingConfig.Contexts[name] = context
	}

	return saveKubeconfigAtomically(kubeconfigPath, existingConfig)
}
