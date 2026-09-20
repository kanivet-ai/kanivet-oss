package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	jsonv2 "encoding/json/v2"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	"cloud.google.com/go/resourcemanager/apiv3/resourcemanagerpb"
	"golang.org/x/oauth2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// GCPProvider discovers GKE clusters using the same identity the user's
// terminal has: the active `gcloud` account. It never writes credentials.
type GCPProvider struct {
	mu                sync.RWMutex
	serviceAccountKey string
	tokenSource       oauth2.TokenSource
	cli               *cliLoginManager
	onAuthChanged     func()
}

func NewGCPProvider() *GCPProvider {
	p := &GCPProvider{cli: newCLILoginManager()}
	p.cli.onDone = func(job *CLILoginJob) {
		p.mu.Lock()
		p.tokenSource = nil
		p.mu.Unlock()
		if p.onAuthChanged != nil {
			p.onAuthChanged()
		}
	}
	return p
}

func (p *GCPProvider) SetOnAuthChanged(fn func()) { p.onAuthChanged = fn }

// gcloudTokenSource mints access tokens through `gcloud auth print-access-token`,
// which refreshes the user's credentials silently, exactly like the
// gke-gcloud-auth-plugin does for kubectl.
type gcloudTokenSource struct{}

func (gcloudTokenSource) Token() (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := runCLI(ctx, "gcloud", []string{"auth", "print-access-token", "--quiet"}, nil)
	if err != nil {
		return nil, fmt.Errorf("gcloud auth print-access-token: %s", humanizeGcloudError(err, out))
	}
	token := strings.TrimSpace(out)
	if token == "" {
		return nil, fmt.Errorf("gcloud returned an empty access token")
	}
	return &oauth2.Token{AccessToken: token, TokenType: "Bearer", Expiry: time.Now().Add(50 * time.Minute)}, nil
}

// runCLI executes a cloud CLI with the augmented PATH and returns combined output.
func runCLI(ctx context.Context, name string, args []string, extraEnv []string) (string, error) {
	path, ok := lookPath(name)
	if !ok {
		return "", &CLIMissingError{Binary: name}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = cliEnv(extraEnv...)
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func humanizeGcloudError(err error, output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "reauthentication required"), strings.Contains(lower, "invalid_grant"), strings.Contains(lower, "token has been expired or revoked"):
		return "your Google Cloud sign-in has expired; run gcloud auth login or sign in from Kanivet"
	case strings.Contains(lower, "do not have an active account"), strings.Contains(lower, "no credentialed accounts"), strings.Contains(lower, "you do not currently have an active account"):
		return "no active gcloud account; sign in with gcloud auth login"
	}
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		lines := strings.Split(trimmed, "\n")
		return strings.TrimSpace(lines[len(lines)-1])
	}
	return err.Error()
}

func (p *GCPProvider) clientOptions() []option.ClientOption {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.serviceAccountKey != "" {
		return []option.ClientOption{option.WithCredentialsFile(p.serviceAccountKey)}
	}
	if _, ok := lookPath("gcloud"); !ok {
		return nil // fall back to Application Default Credentials
	}
	if p.tokenSource == nil {
		p.tokenSource = oauth2.ReuseTokenSource(nil, gcloudTokenSource{})
	}
	return []option.ClientOption{option.WithTokenSource(p.tokenSource)}
}

func (p *GCPProvider) ListProjects(ctx context.Context) ([]GCPProject, error) {
	client, err := resourcemanager.NewProjectsClient(ctx, p.clientOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to create projects client: %w", err)
	}
	defer client.Close()

	var projects []GCPProject
	it := client.SearchProjects(ctx, &resourcemanagerpb.SearchProjectsRequest{})
	for {
		proj, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if proj.State == resourcemanagerpb.Project_ACTIVE {
			projects = append(projects, GCPProject{
				ID:   proj.ProjectId,
				Name: proj.DisplayName,
			})
		}
	}
	return projects, nil
}

type gcloudAccount struct {
	Account string `json:"account"`
	Status  string `json:"status"`
}

// Status reports the active gcloud account and whether its token still works.
func (p *GCPProvider) Status(ctx context.Context) ProviderAuthSummary {
	summary := ProviderAuthSummary{
		Provider:   ProviderGCP,
		CLIName:    "gcloud",
		PluginName: "gke-gcloud-auth-plugin",
		CheckedAt:  time.Now().UnixMilli(),
	}
	_, summary.CLIInstalled = lookPath("gcloud")
	_, summary.PluginInstalled = lookPath("gke-gcloud-auth-plugin")
	if !summary.CLIInstalled {
		summary.CLIInstallHint = cliInstallHint("gcloud")
	}
	if !summary.PluginInstalled {
		summary.PluginInstallHint = cliInstallHint("gke-gcloud-auth-plugin")
	}
	if job, ok := p.cli.running("gcp"); ok {
		summary.Login = job
	}
	if !summary.CLIInstalled {
		summary.State = "unavailable"
		summary.Detail = "Google Cloud CLI not found"
		return summary
	}

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := runCLI(listCtx, "gcloud", []string{"auth", "list", "--format=json"}, nil)
	if err != nil {
		summary.State = "unavailable"
		summary.Error = humanizeGcloudError(err, out)
		return summary
	}
	var accounts []gcloudAccount
	if start := strings.Index(out, "["); start >= 0 {
		_ = jsonv2.Unmarshal([]byte(out[start:]), &accounts)
	}
	for _, acc := range accounts {
		if strings.EqualFold(acc.Status, "ACTIVE") {
			summary.Identity = acc.Account
			break
		}
	}
	if summary.Identity == "" {
		summary.State = "signed_out"
		summary.Detail = "No active gcloud account"
		return summary
	}

	projCtx, cancelProj := context.WithTimeout(ctx, 10*time.Second)
	defer cancelProj()
	if projOut, err := runCLI(projCtx, "gcloud", []string{"config", "get-value", "project", "--quiet"}, nil); err == nil {
		if project := lastNonEmptyLine(projOut); project != "" && !strings.Contains(project, "unset") {
			summary.Detail = "Project " + project
		}
	}

	verifyCtx, cancelVerify := context.WithTimeout(ctx, 20*time.Second)
	defer cancelVerify()
	if tokOut, err := runCLI(verifyCtx, "gcloud", []string{"auth", "print-access-token", "--quiet"}, nil); err != nil {
		summary.State = "expired"
		summary.Error = humanizeGcloudError(err, tokOut)
		return summary
	}
	summary.State = "active"
	summary.SignedIn = true
	return summary
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "WARNING") && !strings.HasPrefix(l, "Updates are available") {
			return l
		}
	}
	return ""
}

func (p *GCPProvider) IsAuthenticated(ctx context.Context) bool {
	return p.Status(ctx).SignedIn
}

// Login runs `gcloud auth login --update-adc` in the background. gcloud opens
// the browser itself; the returned job lets the UI show progress and cancel.
func (p *GCPProvider) Login(ctx context.Context) (*CLILoginJob, error) {
	return p.cli.start("gcp", ProviderGCP, "Google Cloud", "gcloud", []string{"auth", "login", "--update-adc", "--quiet"}, cliEnv())
}

func (p *GCPProvider) LoginJob(id string) (*CLILoginJob, bool) { return p.cli.get(id) }
func (p *GCPProvider) CancelLogin(id string) bool              { return p.cli.cancel(id) }

func (p *GCPProvider) GetCurrentProject(ctx context.Context) (string, error) {
	out, err := runCLI(ctx, "gcloud", []string{"config", "get-value", "project", "--quiet"}, nil)
	if err != nil {
		return "", err
	}
	return lastNonEmptyLine(out), nil
}

func gkeContextName(projectID, location, name string) string {
	return fmt.Sprintf("gke_%s_%s_%s", projectID, location, name)
}

func (p *GCPProvider) DiscoverClusters(ctx context.Context, projectID string, locations []string) ([]DiscoveredCluster, error) {
	client, err := container.NewClusterManagerClient(ctx, p.clientOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GKE client: %w", err)
	}
	defer client.Close()

	if len(locations) == 0 {
		locations = []string{"-"} // "-" means all locations
	}

	var clusters []DiscoveredCluster
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	sem := make(chan struct{}, 5)

	for _, location := range locations {
		wg.Add(1)
		go func(loc string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			parent := fmt.Sprintf("projects/%s/locations/%s", projectID, loc)
			resp, err := client.ListClusters(ctx, &containerpb.ListClustersRequest{Parent: parent})
			if err != nil {
				log.Printf("Error listing GKE clusters in %s: %v", parent, err)
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}

			for _, c := range resp.Clusters {
				cluster := DiscoveredCluster{
					ID:            gkeContextName(projectID, c.Location, c.Name),
					Name:          c.Name,
					Provider:      ProviderGCP,
					Region:        c.Location,
					ProjectID:     projectID,
					Endpoint:      fmt.Sprintf("https://%s", c.Endpoint),
					Version:       c.CurrentMasterVersion,
					Status:        c.Status.String(),
					NodeCount:     int(c.CurrentNodeCount),
					HasAccess:     true,
					AccessChecked: true,
				}
				if c.ResourceLabels != nil {
					cluster.Tags = c.ResourceLabels
				}
				mu.Lock()
				clusters = append(clusters, cluster)
				mu.Unlock()
			}
		}(location)
	}
	wg.Wait()

	if len(clusters) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return clusters, nil
}

func (p *GCPProvider) ImportCluster(ctx context.Context, req ImportRequest) error {
	client, err := container.NewClusterManagerClient(ctx, p.clientOptions()...)
	if err != nil {
		return fmt.Errorf("failed to create GKE client: %w", err)
	}
	defer client.Close()

	clusterPath := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", req.ProjectID, req.Region, req.Name)
	cluster, err := client.GetCluster(ctx, &containerpb.GetClusterRequest{Name: clusterPath})
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	kubeconfigPath := KanivetKubeconfigPath()
	kubeconfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		kubeconfig = clientcmdapi.NewConfig()
	}

	contextName := gkeContextName(req.ProjectID, req.Region, req.Name)
	clusterName := contextName

	caData, _ := base64.StdEncoding.DecodeString(cluster.MasterAuth.ClusterCaCertificate)

	kubeconfig.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   fmt.Sprintf("https://%s", cluster.Endpoint),
		CertificateAuthorityData: caData,
	}

	kubeconfig.AuthInfos[clusterName] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion:         "client.authentication.k8s.io/v1beta1",
			Command:            "gke-gcloud-auth-plugin",
			InstallHint:        cliInstallHint("gke-gcloud-auth-plugin"),
			ProvideClusterInfo: true,
			InteractiveMode:    clientcmdapi.NeverExecInteractiveMode,
		},
	}

	kubeconfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: clusterName,
	}

	return saveKubeconfigAtomically(kubeconfigPath, kubeconfig)
}

// UseServiceAccount points discovery at a service-account key file for this
// process only; nothing is exported to the environment or written to disk.
func (p *GCPProvider) UseServiceAccount(ctx context.Context, keyFilePath string) error {
	p.mu.Lock()
	p.serviceAccountKey = keyFilePath
	p.tokenSource = nil
	p.mu.Unlock()
	return nil
}

func (p *GCPProvider) ListLocations(ctx context.Context, projectID string) ([]string, error) {
	return []string{
		"us-central1", "us-east1", "us-east4", "us-east5", "us-south1", "us-west1", "us-west2", "us-west3", "us-west4",
		"northamerica-northeast1", "northamerica-northeast2", "southamerica-east1", "southamerica-west1",
		"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6", "europe-west8", "europe-west9",
		"europe-west10", "europe-west12", "europe-central2", "europe-north1", "europe-southwest1",
		"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3",
		"asia-south1", "asia-south2", "asia-southeast1", "asia-southeast2",
		"australia-southeast1", "australia-southeast2", "me-central1", "me-central2", "me-west1", "africa-south1",
	}, nil
}
