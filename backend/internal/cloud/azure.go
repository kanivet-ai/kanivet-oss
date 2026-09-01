package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type AzureProvider struct {
	cred *azidentity.DefaultAzureCredential
	mu   sync.RWMutex
}

func NewAzureProvider() *AzureProvider {
	return &AzureProvider{}
}

func (p *AzureProvider) ensureCredential() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cred != nil {
		return nil
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
			return nil, err
		}
		for _, sub := range page.Value {
			subs = append(subs, AzureSubscription{
				ID:       *sub.SubscriptionID,
				Name:     *sub.DisplayName,
				TenantID: *sub.TenantID,
				State:    string(*sub.State),
			})
		}
	}
	return subs, nil
}

func (p *AzureProvider) IsAuthenticated(ctx context.Context) bool {
	if err := p.ensureCredential(); err != nil {
		return false
	}
	client, err := armsubscriptions.NewClient(p.cred, nil)
	if err != nil {
		return false
	}
	pager := client.NewListPager(nil)
	_, err = pager.NextPage(ctx)
	return err == nil
}

func (p *AzureProvider) Login(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "az", "login")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("az login failed: %w", err)
	}
	p.mu.Lock()
	p.cred = nil
	p.mu.Unlock()
	return p.ensureCredential()
}

func (p *AzureProvider) LoginWithServicePrincipal(ctx context.Context, tenantID, clientID, clientSecret string) error {
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return fmt.Errorf("failed to create service principal credential: %w", err)
	}
	_ = cred
	return nil
}

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
			log.Printf("Error listing AKS clusters: %v", err)
			break
		}
		for _, c := range page.Value {
			rg := extractResourceGroup(*c.ID)
			cluster := DiscoveredCluster{
				ID:            *c.ID,
				Name:          *c.Name,
				Provider:      ProviderAzure,
				Region:        *c.Location,
				AccountID:     subscriptionID,
				ResourceGroup: rg,
				Status:        string(*c.Properties.ProvisioningState),
			}
			if c.Properties.KubernetesVersion != nil {
				cluster.Version = *c.Properties.KubernetesVersion
			}
			if c.Properties.Fqdn != nil {
				cluster.Endpoint = fmt.Sprintf("https://%s", *c.Properties.Fqdn)
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
	parts := make([]string, 0)
	current := ""
	for _, c := range resourceID {
		if c == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func (p *AzureProvider) ImportCluster(ctx context.Context, req ImportRequest) error {
	if err := p.ensureCredential(); err != nil {
		return err
	}

	client, err := armcontainerservice.NewManagedClustersClient(req.AccountID, p.cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create AKS client: %w", err)
	}

	credsResp, err := client.ListClusterAdminCredentials(ctx, req.ResourceGroup, req.Name, nil)
	if err != nil {
		credsResp2, err := client.ListClusterUserCredentials(ctx, req.ResourceGroup, req.Name, nil)
		if err != nil {
			return fmt.Errorf("failed to get cluster credentials: %w", err)
		}
		return p.mergeKubeconfig(credsResp2.Kubeconfigs)
	}
	return p.mergeKubeconfig(credsResp.Kubeconfigs)
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
		existingConfig.AuthInfos[name] = authInfo
	}
	for name, context := range newConfig.Contexts {
		existingConfig.Contexts[name] = context
	}

	return clientcmd.WriteToFile(*existingConfig, kubeconfigPath)
}

func (p *AzureProvider) GetClusterCredentials(ctx context.Context, subscriptionID, resourceGroup, clusterName string, admin bool) error {
	args := []string{"aks", "get-credentials",
		"--subscription", subscriptionID,
		"--resource-group", resourceGroup,
		"--name", clusterName,
		"--overwrite-existing"}
	if admin {
		args = append(args, "--admin")
	}
	cmd := exec.CommandContext(ctx, "az", args...)
	cmd.Env = os.Environ()
	return cmd.Run()
}

func (p *AzureProvider) ImportClusterWithKubelogin(ctx context.Context, req ImportRequest) error {
	if err := p.ensureCredential(); err != nil {
		return err
	}

	client, err := armcontainerservice.NewManagedClustersClient(req.AccountID, p.cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create AKS client: %w", err)
	}

	cluster, err := client.Get(ctx, req.ResourceGroup, req.Name, nil)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	kubeconfigPath := KanivetKubeconfigPath()
	kubeconfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		kubeconfig = clientcmdapi.NewConfig()
	}

	contextName := req.Name
	clusterName := req.Name

	var caData []byte
	if cluster.Properties.AADProfile != nil && cluster.Properties.AADProfile.Managed != nil && *cluster.Properties.AADProfile.Managed {
		caData, _ = base64.StdEncoding.DecodeString("")
	}

	kubeconfig.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   fmt.Sprintf("https://%s", *cluster.Properties.Fqdn),
		CertificateAuthorityData: caData,
	}

	kubeconfig.AuthInfos[clusterName] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Command:    "kubelogin",
			Args: []string{
				"get-token",
				"--environment", "AzurePublicCloud",
				"--server-id", "6dae42f8-4368-4678-94ff-3960e28e3630",
				"--login", "azurecli",
			},
		},
	}

	kubeconfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: clusterName,
	}

	return clientcmd.WriteToFile(*kubeconfig, kubeconfigPath)
}
