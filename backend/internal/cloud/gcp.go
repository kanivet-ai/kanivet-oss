package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	"cloud.google.com/go/resourcemanager/apiv3/resourcemanagerpb"
	"google.golang.org/api/iterator"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type GCPProvider struct {
	mu sync.RWMutex
}

func NewGCPProvider() *GCPProvider {
	return &GCPProvider{}
}

func (p *GCPProvider) ListProjects(ctx context.Context) ([]GCPProject, error) {
	client, err := resourcemanager.NewProjectsClient(ctx)
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

func (p *GCPProvider) IsAuthenticated(ctx context.Context) bool {
	client, err := resourcemanager.NewProjectsClient(ctx)
	if err != nil {
		return false
	}
	defer client.Close()

	it := client.SearchProjects(ctx, &resourcemanagerpb.SearchProjectsRequest{})
	_, err = it.Next()
	return err == nil || err == iterator.Done
}

func (p *GCPProvider) Login(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gcloud", "auth", "application-default", "login")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (p *GCPProvider) GetCurrentProject(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gcloud", "config", "get-value", "project")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (p *GCPProvider) DiscoverClusters(ctx context.Context, projectID string, locations []string) ([]DiscoveredCluster, error) {
	client, err := container.NewClusterManagerClient(ctx)
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
				log.Printf("Error listing clusters in %s: %v", loc, err)
				return
			}

			for _, c := range resp.Clusters {
				cluster := DiscoveredCluster{
					ID:        c.SelfLink,
					Name:      c.Name,
					Provider:  ProviderGCP,
					Region:    c.Location,
					ProjectID: projectID,
					Endpoint:  fmt.Sprintf("https://%s", c.Endpoint),
					Version:   c.CurrentMasterVersion,
					Status:    c.Status.String(),
					NodeCount: int(c.CurrentNodeCount),
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

	return clusters, nil
}

func (p *GCPProvider) ImportCluster(ctx context.Context, req ImportRequest) error {
	client, err := container.NewClusterManagerClient(ctx)
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

	contextName := fmt.Sprintf("gke_%s_%s_%s", req.ProjectID, req.Region, req.Name)
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
			InstallHint:        "Install gke-gcloud-auth-plugin for use with kubectl by following https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-access-for-kubectl#install_plugin",
			ProvideClusterInfo: true,
		},
	}

	kubeconfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: clusterName,
	}

	return clientcmd.WriteToFile(*kubeconfig, kubeconfigPath)
}

func (p *GCPProvider) GetClusterCredentials(ctx context.Context, projectID, location, clusterName string) error {
	cmd := exec.CommandContext(ctx, "gcloud", "container", "clusters", "get-credentials",
		clusterName,
		"--project", projectID,
		"--region", location)
	cmd.Env = os.Environ()
	return cmd.Run()
}

func (p *GCPProvider) UseServiceAccount(ctx context.Context, keyFilePath string) error {
	return os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", keyFilePath)
}

func (p *GCPProvider) ListLocations(ctx context.Context, projectID string) ([]string, error) {
	return []string{
		"us-central1", "us-east1", "us-east4", "us-west1", "us-west2", "us-west3", "us-west4",
		"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6",
		"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3",
		"asia-south1", "asia-southeast1", "asia-southeast2",
		"australia-southeast1", "australia-southeast2",
		"southamerica-east1", "northamerica-northeast1",
	}, nil
}
