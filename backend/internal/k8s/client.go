package k8s

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/utils"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Interface defines the behavior required from a Kubernetes client. This
// improves modularity and testability by allowing the API layer to depend on
// an abstraction rather than the concrete client implementation.
type Interface interface {
	ListClusters() ([]ClusterInfo, error)
	GetClientForCluster(cluster string) (kubernetes.Interface, error)
	GetClientAndConfig(cluster string) (kubernetes.Interface, *rest.Config, error)
	GetDynamicClient(cluster string) (dynamic.Interface, error)
	GetInteractiveDynamicClient(cluster string) (dynamic.Interface, error)
	GetMetadataClient(cluster string) (metadata.Interface, error)
	GetBulkMetadataClient(cluster string) (metadata.Interface, error)
	GetDiscoveryClient(cluster string) (discovery.DiscoveryInterface, error)
	ResolveKindToResource(cluster, group, version, kind string) (string, error)
	GetResourceName(cluster, group, version, kind string) string

	ListAPIResources(cluster string) ([]metav1.APIResource, error)

	GetClusterStatus(cluster string) (*ClusterStatus, error)
	ListNamespaces(cluster string) ([]string, error)

	ScaleResource(ctx context.Context, cluster, group, version, kind, namespace, name string, replicas int32) error
	TaintNode(ctx context.Context, cluster, nodeName string, key, value, effect string) error
	RemoveTaint(ctx context.Context, cluster, nodeName, key string) error
	DrainNode(ctx context.Context, cluster, nodeName string, ignoreDaemonsets, deleteEmptyDir bool, gracePeriod int) (*DrainResult, error)
	CordonNode(ctx context.Context, cluster, nodeName string, unschedulable bool) error
	RestartResource(ctx context.Context, cluster, group, version, kind, namespace, name string) error
	TriggerCronJob(ctx context.Context, cluster, namespace, name string) (string, error)
	GetRolloutStatus(ctx context.Context, cluster, group, version, kind, namespace, name string) (*RolloutStatus, error)
	GetBatchRolloutStatus(ctx context.Context, cluster string, requests []RolloutStatusRequest) []BatchRolloutStatus

	GetResourceCount(ctx context.Context, cluster string, gvr schema.GroupVersionResource) (int, error)
	UpdateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)
	CreateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)

	CreatePortForward(cluster, namespace, podName string, remotePort int) (*PortForward, error)
	StopPortForward(id string) error
	GetPortForward(id string) (*PortForward, bool)

	GetPodLogs(ctx context.Context, cluster, namespace, name, container, tailLines string) (string, error)

	GetCrossplaneXRDs(cluster string) ([]map[string]interface{}, error)
	IsCrossplaneResource(cluster string, group string, version string, kind string) (bool, error)

	RefreshClusterCache(cluster string)

	ListVClusters(host string) ([]VClusterInfo, error)
	ConnectVCluster(host, namespace, name string) (*VClusterConnection, error)
	DisconnectVCluster(id string) error
}

type RolloutStatus struct {
	Status             string                   `json:"status"`
	Message            string                   `json:"message"`
	Replicas           int32                    `json:"replicas"`
	UpdatedReplicas    int32                    `json:"updatedReplicas"`
	ReadyReplicas      int32                    `json:"readyReplicas"`
	AvailableReplicas  int32                    `json:"availableReplicas"`
	ObservedGeneration int64                    `json:"observedGeneration"`
	Conditions         []map[string]interface{} `json:"conditions"`
}

type RolloutStatusRequest struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type BatchRolloutStatus struct {
	Key    string         `json:"key"`
	Status *RolloutStatus `json:"status,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type Client struct {
	configs             map[string]*rest.Config
	clients             map[string]kubernetes.Interface
	dynamic             map[string]dynamic.Interface
	interactive         map[string]dynamic.Interface
	metadata            map[string]metadata.Interface
	bulkMetadata        map[string]metadata.Interface
	discovery           map[string]discovery.DiscoveryInterface
	vclusterCache       map[string]*VClusterConnection
	VClusterSupervisors map[string]*VClusterSupervisor
	vclusterStatusSubs  map[uint64]chan VClusterStatus
	vclusterStatusMu    sync.RWMutex
	vclusterStatusSubID uint64
	kubeconfigs         []string
	mu                  sync.RWMutex
	portForwardManager  *PortForwardManager
	resourceNameCache   map[string]string
	resourceNameMu      sync.RWMutex

	contextIndexMu    sync.RWMutex
	contextIndex      map[string]string
	contextIndexStamp map[string]int64
}

func (c *Client) invalidateContextIndex() {
	c.contextIndexMu.Lock()
	c.contextIndex = nil
	c.contextIndexStamp = nil
	c.contextIndexMu.Unlock()
}

var excludedExtensions = map[string]bool{
	".backup": true, ".bak": true, ".tmp": true, ".old": true,
	".lock": true, ".swp": true, ".swo": true,
}

var excludedNames = map[string]bool{
	"cache": true, "http-cache": true, "kubectx": true, "kubens": true,
}

func looksLikeKubeconfig(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	content := string(buf[:n])
	return strings.Contains(content, "clusters:") || strings.Contains(content, "contexts:") || strings.Contains(content, "current-context:")
}

func discoverKubeconfigs() []string {
	start := time.Now()
	seen := make(map[string]bool)
	var configs []string
	addConfig := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil || seen[abs] {
			return
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return
		}
		if !looksLikeKubeconfig(abs) {
			return
		}
		seen[abs] = true
		configs = append(configs, abs)
	}
	if envPath := os.Getenv("KUBECONFIG"); envPath != "" {
		sep := ":"
		if strings.Contains(envPath, ";") {
			sep = ";"
		}
		for _, p := range strings.Split(envPath, sep) {
			if p = strings.TrimSpace(p); p != "" {
				addConfig(p)
			}
		}
	}
	home, _ := os.UserHomeDir()
	kubeDir := filepath.Join(home, ".kube")
	entries, err := os.ReadDir(kubeDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || entry.IsDir() {
				continue
			}
			if excludedNames[name] || excludedExtensions[filepath.Ext(name)] {
				continue
			}
			addConfig(filepath.Join(kubeDir, name))
		}
	}
	if len(configs) == 0 {
		defaultPath := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(defaultPath); err == nil {
			configs = append(configs, defaultPath)
		}
	}
	log.Printf("[K8S] Discovered %d kubeconfig file(s) in %v", len(configs), time.Since(start))
	return configs
}

func NewClient() *Client {
	c := &Client{
		configs:             make(map[string]*rest.Config),
		clients:             make(map[string]kubernetes.Interface),
		dynamic:             make(map[string]dynamic.Interface),
		interactive:         make(map[string]dynamic.Interface),
		metadata:            make(map[string]metadata.Interface),
		bulkMetadata:        make(map[string]metadata.Interface),
		discovery:           make(map[string]discovery.DiscoveryInterface),
		vclusterCache:       make(map[string]*VClusterConnection),
		VClusterSupervisors: make(map[string]*VClusterSupervisor),
		vclusterStatusSubs:  make(map[uint64]chan VClusterStatus),
		kubeconfigs:         discoverKubeconfigs(),
		resourceNameCache:   make(map[string]string),
	}
	c.portForwardManager = NewPortForwardManager(c)

	return c
}

// RefreshClusterCache fully resets all cached state for a cluster (or every
// cluster when cluster=""). Use this when kubeconfig contents may have changed
// — manual user refresh, cloud-import batch completion, auth errors, or
// credential-source changes detected by the cloud config watcher.
func (c *Client) RefreshClusterCache(cluster string) {
	c.mu.Lock()
	if cluster == "" {
		c.kubeconfigs = discoverKubeconfigs()
		c.invalidateContextIndex()
		clear(c.configs)
		clear(c.clients)
		clear(c.dynamic)
		clear(c.interactive)
		clear(c.metadata)
		clear(c.bulkMetadata)
		clear(c.discovery)
	} else {
		delete(c.configs, cluster)
		delete(c.clients, cluster)
		delete(c.dynamic, cluster)
		delete(c.interactive, cluster)
		delete(c.metadata, cluster)
		delete(c.bulkMetadata, cluster)
		delete(c.discovery, cluster)
	}
	c.mu.Unlock()
	c.resourceNameMu.Lock()
	if cluster == "" {
		clear(c.resourceNameCache)
	} else {
		prefix := cluster + ":"
		for k := range c.resourceNameCache {
			if len(k) > len(prefix) && k[:len(prefix)] == prefix {
				delete(c.resourceNameCache, k)
			}
		}
	}
	c.resourceNameMu.Unlock()
}

func getOrCreate[T any](cache map[string]T, key string, creator func() (T, error), mu *sync.RWMutex) (T, error) {
	mu.RLock()
	if client, ok := cache[key]; ok {
		mu.RUnlock()
		return client, nil
	}
	mu.RUnlock()

	mu.Lock()
	if client, ok := cache[key]; ok {
		mu.Unlock()
		return client, nil
	}
	mu.Unlock()

	client, err := creator()
	if err != nil {
		var zero T
		return zero, err
	}
	mu.Lock()
	defer mu.Unlock()
	cache[key] = client
	return client, nil
}

type ClusterInfo struct {
	Name       string `json:"name"`
	Kubeconfig string `json:"kubeconfig"`
}

func (c *Client) ListClusters() ([]ClusterInfo, error) {
	start := time.Now()
	index := c.contextToKubeconfigIndex()
	clusters := make([]ClusterInfo, 0, len(index))
	for name, path := range index {
		clusters = append(clusters, ClusterInfo{Name: name, Kubeconfig: path})
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Name < clusters[j].Name })
	log.Printf("[K8S] ListClusters found %d clusters in %v", len(clusters), time.Since(start))
	return clusters, nil
}

func (c *Client) findKubeconfigForContext(contextName string) string {
	return c.contextToKubeconfigIndex()[contextName]
}

// contextToKubeconfigIndex returns a map of every kubeconfig context name to the
// path of the kubeconfig file that defines it. Result is memoized and reused
// until any kubeconfig file's mtime changes (or the discovered file list changes),
// so repeated lookups during a status-poll wave do not re-parse YAML on every call.
func (c *Client) contextToKubeconfigIndex() map[string]string {
	c.mu.RLock()
	paths := make([]string, len(c.kubeconfigs))
	copy(paths, c.kubeconfigs)
	c.mu.RUnlock()

	stamp := make(map[string]int64, len(paths))
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			stamp[p] = info.ModTime().UnixNano()
		} else {
			stamp[p] = -1
		}
	}

	c.contextIndexMu.RLock()
	if c.contextIndex != nil && stampsEqual(c.contextIndexStamp, stamp) {
		idx := c.contextIndex
		c.contextIndexMu.RUnlock()
		return idx
	}
	c.contextIndexMu.RUnlock()

	index := make(map[string]string)
	for _, path := range paths {
		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			log.Printf("Skipping kubeconfig %s: %v", path, err)
			continue
		}
		for ctx := range cfg.Contexts {
			if _, exists := index[ctx]; !exists {
				index[ctx] = path
			}
		}
	}

	c.contextIndexMu.Lock()
	c.contextIndex = index
	c.contextIndexStamp = stamp
	c.contextIndexMu.Unlock()

	return index
}

func stampsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func (c *Client) getConfigForCluster(cluster string) (*rest.Config, error) {
	if IsVClusterID(cluster) {
		return c.ensureVClusterAlive(cluster)
	}
	return getOrCreate(c.configs, cluster, func() (*rest.Config, error) {
		kubeconfigPath := c.findKubeconfigForContext(cluster)
		if kubeconfigPath == "" {
			return nil, fmt.Errorf("context %s not found in any kubeconfig", cluster)
		}
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
		configOverrides := &clientcmd.ConfigOverrides{CurrentContext: cluster}
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules, configOverrides)

		// Get the raw config first to check for exec providers
		rawConfig, err := clientConfig.RawConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load raw config for cluster %s: %w", cluster, err)
		}

		// Check if this context uses an exec provider (AWS EKS, GCP GKE, etc)
		if context, ok := rawConfig.Contexts[cluster]; ok {
			if authInfo, ok := rawConfig.AuthInfos[context.AuthInfo]; ok && authInfo.Exec != nil {
				log.Printf("Cluster %s uses exec plugin: %s", cluster, authInfo.Exec.Command)

				// Ensure PATH environment variable is available for exec plugins
				if authInfo.Exec.Env == nil {
					authInfo.Exec.Env = []clientcmdapi.ExecEnvVar{}
				}

				// Check if PATH is already set
				pathSet := false
				for _, env := range authInfo.Exec.Env {
					if env.Name == "PATH" {
						pathSet = true
						break
					}
				}

				// Add PATH if not set (needed for aws, gcloud, etc.)
				if !pathSet {
					currentPath := os.Getenv("PATH")
					enhancedPath := currentPath
					commonPaths := []string{
						"/usr/local/bin",
						"/opt/homebrew/bin",
						"/usr/bin",
						"/snap/bin", // Snap packages on Linux
					}
					homeDir := os.Getenv("HOME")
					if homeDir != "" {
						commonPaths = append(commonPaths,
							homeDir+"/google-cloud-sdk/bin",
							homeDir+"/.local/bin",
						)
					}
					commonPaths = append(commonPaths,
						// Homebrew cask installations
						"/usr/local/Caskroom/google-cloud-sdk/latest/google-cloud-sdk/bin",
						"/opt/homebrew/Caskroom/google-cloud-sdk/latest/google-cloud-sdk/bin",
						// Homebrew formula installations (brew install google-cloud-sdk)
						"/opt/homebrew/share/google-cloud-sdk/bin",
						"/usr/local/share/google-cloud-sdk/bin",
						// Azure CLI on Debian/Ubuntu
						"/opt/az/bin",
					)
					for _, p := range commonPaths {
						if !strings.Contains(enhancedPath, p) {
							enhancedPath = enhancedPath + ":" + p
						}
					}
					authInfo.Exec.Env = append(authInfo.Exec.Env, clientcmdapi.ExecEnvVar{
						Name:  "PATH",
						Value: enhancedPath,
					})
				}

				// Pass through important cloud environment variables if they exist
				cloudEnvVars := []string{
					"AWS_PROFILE", "AWS_DEFAULT_REGION", "AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
					"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CORE_PROJECT", "CLOUDSDK_COMPUTE_REGION", "CLOUDSDK_COMPUTE_ZONE", "CLOUDSDK_CONFIG", "CLOUDSDK_PYTHON", "USE_GKE_GCLOUD_AUTH_PLUGIN",
				}
				for _, envVar := range cloudEnvVars {
					varSet := false
					for _, env := range authInfo.Exec.Env {
						if env.Name == envVar {
							varSet = true
							break
						}
					}
					if !varSet {
						if value := os.Getenv(envVar); value != "" {
							authInfo.Exec.Env = append(authInfo.Exec.Env, clientcmdapi.ExecEnvVar{
								Name:  envVar,
								Value: value,
							})
						}
					}
				}
			}
		}

		config, err := clientConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create config for cluster %s: %w", cluster, err)
		}

		// Increase rate limits to handle multiple watchers and background indexing
		// Default is 5 QPS and 10 Burst which is too low for our use case
		config.QPS = 50.0  // Allow 50 queries per second
		config.Burst = 100 // Allow bursts up to 100

		// Prefer protobuf for typed and metadata clients. Unstructured/dynamic
		// clients ignore this and stay on JSON; protobuf shrinks list payloads
		// and Unmarshal cost by an order of magnitude for native kinds.
		config.ContentType = "application/vnd.kubernetes.protobuf"
		config.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"

		// Note: We don't set config.Timeout here because it would affect streaming operations
		// like log following and watch operations that need to stay open indefinitely

		log.Printf("Configured Kubernetes client for cluster %s with QPS=%f, Burst=%d", cluster, config.QPS, config.Burst)

		return config, nil
	}, &c.mu)
}

func (c *Client) GetClientForCluster(cluster string) (kubernetes.Interface, error) {
	if IsVClusterID(cluster) {
		if _, err := c.ensureVClusterAlive(cluster); err != nil {
			return nil, err
		}
		return c.buildVClusterClient(cluster)
	}
	return getOrCreate(c.clients, cluster, func() (kubernetes.Interface, error) {
		config, err := c.getConfigForCluster(cluster)
		if err != nil {
			return nil, err
		}
		return kubernetes.NewForConfig(config)
	}, &c.mu)
}

func (c *Client) buildVClusterClient(cluster string) (kubernetes.Interface, error) {
	c.mu.RLock()
	if existing, ok := c.clients[cluster]; ok {
		c.mu.RUnlock()
		return existing, nil
	}
	c.mu.RUnlock()
	cfg, err := c.getConfigForCluster(cluster)
	if err != nil {
		return nil, err
	}
	cl, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if existing, ok := c.clients[cluster]; ok {
		c.mu.Unlock()
		return existing, nil
	}
	c.clients[cluster] = cl
	c.mu.Unlock()
	return cl, nil
}

func (c *Client) GetClientAndConfig(cluster string) (kubernetes.Interface, *rest.Config, error) {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return nil, nil, err
	}
	config, err := c.getConfigForCluster(cluster)
	if err != nil {
		return nil, nil, err
	}
	return client, config, nil
}

func (c *Client) GetDynamicClient(cluster string) (dynamic.Interface, error) {
	if IsVClusterID(cluster) {
		if _, err := c.ensureVClusterAlive(cluster); err != nil {
			return nil, err
		}
	}
	return getOrCreate(c.dynamic, cluster, func() (dynamic.Interface, error) {
		config, err := c.getConfigForCluster(cluster)
		if err != nil {
			return nil, err
		}
		return dynamic.NewForConfig(config)
	}, &c.mu)
}

// GetInteractiveDynamicClient returns a dynamic client with its own dedicated
// connection to the apiserver, so user-facing requests (detail views) never
// queue behind bulk watch/list/reindex traffic on the shared transport.
func (c *Client) GetInteractiveDynamicClient(cluster string) (dynamic.Interface, error) {
	if IsVClusterID(cluster) {
		if _, err := c.ensureVClusterAlive(cluster); err != nil {
			return nil, err
		}
	}
	return getOrCreate(c.interactive, cluster, func() (dynamic.Interface, error) {
		config, err := c.getConfigForCluster(cluster)
		if err != nil {
			return nil, err
		}
		interactiveConfig := *config
		interactiveConfig.Dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		return dynamic.NewForConfig(&interactiveConfig)
	}, &c.mu)
}

func (c *Client) GetMetadataClient(cluster string) (metadata.Interface, error) {
	if IsVClusterID(cluster) {
		if _, err := c.ensureVClusterAlive(cluster); err != nil {
			return nil, err
		}
	}
	return getOrCreate(c.metadata, cluster, func() (metadata.Interface, error) {
		config, err := c.getConfigForCluster(cluster)
		if err != nil {
			return nil, err
		}
		return metadata.NewForConfig(config)
	}, &c.mu)
}

// GetBulkMetadataClient returns a metadata client with its own dedicated
// connection, keeping high-volume background listing (search indexing) off the
// shared transport so it cannot starve user-facing requests.
func (c *Client) GetBulkMetadataClient(cluster string) (metadata.Interface, error) {
	if IsVClusterID(cluster) {
		if _, err := c.ensureVClusterAlive(cluster); err != nil {
			return nil, err
		}
	}
	return getOrCreate(c.bulkMetadata, cluster, func() (metadata.Interface, error) {
		config, err := c.getConfigForCluster(cluster)
		if err != nil {
			return nil, err
		}
		bulkConfig := *config
		bulkConfig.Dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		return metadata.NewForConfig(&bulkConfig)
	}, &c.mu)
}

func (c *Client) GetDiscoveryClient(cluster string) (discovery.DiscoveryInterface, error) {
	if IsVClusterID(cluster) {
		if _, err := c.ensureVClusterAlive(cluster); err != nil {
			return nil, err
		}
	}
	return getOrCreate(c.discovery, cluster, func() (discovery.DiscoveryInterface, error) {
		config, err := c.getConfigForCluster(cluster)
		if err != nil {
			return nil, err
		}
		return discovery.NewDiscoveryClientForConfig(config)
	}, &c.mu)
}

func (c *Client) ListAPIResources(cluster string) ([]metav1.APIResource, error) {
	log.Println("Listing API resources for cluster", cluster)
	config, err := c.getConfigForCluster(cluster)
	if err != nil {
		return nil, err
	}
	// Bounded per-request timeout: the shared config deliberately has none so
	// streams stay open, but discovery must not hang on a dead cluster.
	configCopy := *config
	configCopy.Timeout = 60 * time.Second
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(&configCopy)
	if err != nil {
		return nil, err
	}

	// ServerGroupsAndResources uses aggregated discovery on K8s >= 1.26: every
	// group and version in a single round trip, with automatic per-group
	// fallback on older servers. All versions are returned, not just preferred.
	_, resourceLists, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		if len(resourceLists) == 0 {
			return nil, fmt.Errorf("failed to get server resources: %w", err)
		}
		log.Printf("ListAPIResources partial discovery failure for cluster %s: %v", cluster, err)
	}

	var resources []metav1.APIResource
	for _, resourceList := range resourceLists {
		if resourceList == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(resourceList.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range resourceList.APIResources {
			if !strings.Contains(r.Name, "/") {
				r.Group = gv.Group
				r.Version = gv.Version
				resources = append(resources, r)
			}
		}
	}

	// Pre-warm the kind→resource-name cache so watch setup and detail lookups
	// never pay a live discovery round-trip on first open of a kind.
	c.resourceNameMu.Lock()
	for _, r := range resources {
		prefix := cluster + ":" + r.Group + ":" + r.Version + ":"
		c.resourceNameCache[prefix+r.Kind] = r.Name
		c.resourceNameCache[prefix+strings.ToLower(r.Kind)] = r.Name
		c.resourceNameCache[prefix+r.Name] = r.Name
	}
	c.resourceNameMu.Unlock()

	return resources, nil
}

func (c *Client) ResolveKindToResource(cluster, group, version, kind string) (string, error) {
	discoveryClient, err := c.GetDiscoveryClient(cluster)
	if err != nil {
		return "", err
	}

	gv := version
	if group != "" {
		gv = group + "/" + version
	}

	resourceList, err := discoveryClient.ServerResourcesForGroupVersion(gv)
	if err != nil {
		return "", err
	}

	kindLower := strings.ToLower(kind)
	kindNorm := strings.ReplaceAll(kindLower, "s", "")
	kindLen := len(kindLower)
	for _, r := range resourceList.APIResources {
		if strings.EqualFold(r.Kind, kind) || strings.EqualFold(r.Name, kind) {
			return r.Name, nil
		}
		nameLower := strings.ToLower(r.Name)
		nameNorm := strings.ReplaceAll(nameLower, "s", "")
		lenDiff := kindLen - len(nameLower)
		if lenDiff < 0 {
			lenDiff = -lenDiff
		}
		if kindNorm == nameNorm && lenDiff <= 2 {
			return r.Name, nil
		}
	}

	for _, r := range resourceList.APIResources {
		if strings.EqualFold(r.Name, kind) {
			return r.Name, nil
		}
	}

	return "", fmt.Errorf("resource not found for kind %s in %s", kind, gv)
}

// GetResourceName returns the correct resource name for a given kind.
// It first tries to resolve it via the discovery API, and falls back to pluralization if that fails.
func (c *Client) GetResourceName(cluster, group, version, kind string) string {
	cacheKey := cluster + ":" + group + ":" + version + ":" + kind
	c.resourceNameMu.RLock()
	if cached, ok := c.resourceNameCache[cacheKey]; ok {
		c.resourceNameMu.RUnlock()
		return cached
	}
	c.resourceNameMu.RUnlock()
	resourceName, err := c.ResolveKindToResource(cluster, group, version, kind)
	if err != nil {
		resourceName = utils.PluralizeKind(kind)
	}
	c.resourceNameMu.Lock()
	c.resourceNameCache[cacheKey] = resourceName
	c.resourceNameMu.Unlock()
	return resourceName
}

type ClusterStatus struct {
	Name           string `json:"name"`
	Healthy        bool   `json:"healthy"`
	Error          string `json:"error,omitempty"`
	Version        string `json:"version,omitempty"`
	Platform       string `json:"platform,omitempty"`
	GitVersion     string `json:"gitVersion,omitempty"`
	ResponseTimeMs int64  `json:"responseTimeMs"`
	NodeCount      int    `json:"nodeCount,omitempty"`
	NamespaceCount int    `json:"namespaceCount,omitempty"`
	Provider       string `json:"provider,omitempty"` // aws, gcp, azure, or other
	Region         string `json:"region,omitempty"`   // Cloud region if detectable
}

// detectCloudProvider determines the cloud provider based on the API server URL and context name
func detectCloudProvider(serverURL string, contextName string) (provider string, region string) {
	serverLower := strings.ToLower(serverURL)

	// AWS EKS: *.eks.amazonaws.com or *.eks.<region>.amazonaws.com
	if strings.Contains(serverLower, ".eks.amazonaws.com") {
		// Try to extract region from URL like https://<id>.<region>.eks.amazonaws.com
		parts := strings.Split(serverLower, ".")
		for i, part := range parts {
			if part == "eks" && i > 1 {
				region = parts[i-1]
				break
			}
		}
		// Also check ARN pattern in context name
		if region == "" && strings.HasPrefix(contextName, "arn:aws:eks:") {
			arnParts := strings.Split(contextName, ":")
			if len(arnParts) >= 4 {
				region = arnParts[3]
			}
		}
		return "aws", region
	}

	// GCP GKE: *.gke.io or container.googleapis.com
	if strings.Contains(serverLower, ".gke.io") || strings.Contains(serverLower, "container.googleapis.com") {
		// Try to extract region from context name like gke_project_region_cluster
		if strings.HasPrefix(contextName, "gke_") {
			parts := strings.Split(contextName, "_")
			if len(parts) >= 3 {
				region = parts[2]
			}
		}
		return "gcp", region
	}

	// Azure AKS: *.azmk8s.io or *.hcp.<region>.azmk8s.io
	if strings.Contains(serverLower, ".azmk8s.io") || strings.Contains(serverLower, "azure") {
		// Try to extract region from URL like https://<name>-<id>.hcp.<region>.azmk8s.io
		if strings.Contains(serverLower, ".hcp.") {
			parts := strings.Split(serverLower, ".")
			for i, part := range parts {
				if part == "hcp" && i+1 < len(parts) {
					region = parts[i+1]
					break
				}
			}
		}
		return "azure", region
	}

	// Check context name patterns for fallback detection
	if strings.HasPrefix(contextName, "arn:aws:eks:") {
		arnParts := strings.Split(contextName, ":")
		if len(arnParts) >= 4 {
			region = arnParts[3]
		}
		return "aws", region
	}

	if strings.HasPrefix(contextName, "gke_") {
		parts := strings.Split(contextName, "_")
		if len(parts) >= 3 {
			region = parts[2]
		}
		return "gcp", region
	}

	return "other", ""
}

func (c *Client) GetClusterStatus(cluster string) (*ClusterStatus, error) {
	startTime := time.Now()
	status := &ClusterStatus{
		Name:    cluster,
		Healthy: false,
	}

	// Detect cloud provider from config before making API calls
	c.mu.RLock()
	config := c.configs[cluster]
	c.mu.RUnlock()

	if config != nil {
		provider, region := detectCloudProvider(config.Host, cluster)
		status.Provider = provider
		status.Region = region
	}

	_, config, err := c.GetClientAndConfig(cluster)
	if err != nil {
		status.Error = fmt.Sprintf("Failed to create client: %v", err)
		status.ResponseTimeMs = time.Since(startTime).Milliseconds()
		return status, nil
	}

	// Cold-start health probes can be slow: AWS exec credential plugins alone
	// frequently take 1-2s, and a large cluster's namespace list can take another
	// second. Run the three apiserver calls in parallel under a generous shared
	// timeout so a healthy-but-slow cluster doesn't show as "Connection Timeout".
	timeout := 15 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	configCopy := *config
	configCopy.Timeout = timeout
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(&configCopy)
	if err != nil {
		status.Error = fmt.Sprintf("Failed to create discovery client: %v", err)
		status.ResponseTimeMs = time.Since(startTime).Milliseconds()
		return status, nil
	}

	type versionResult struct {
		info *kversion.Info
		err  error
	}
	type listResult struct {
		count int
		err   error
	}
	versionCh := make(chan versionResult, 1)
	nodesCh := make(chan listResult, 1)
	nsCh := make(chan listResult, 1)

	go func() {
		v, e := discoveryClient.ServerVersion()
		versionCh <- versionResult{v, e}
	}()
	// Metadata-only counts: full Node objects run to ~100KB each and were being
	// downloaded on every probe just to be counted.
	go func() {
		n, e := c.GetResourceCount(ctx, cluster, schema.GroupVersionResource{Version: "v1", Resource: "nodes"})
		nodesCh <- listResult{n, e}
	}()
	go func() {
		n, e := c.GetResourceCount(ctx, cluster, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
		nsCh <- listResult{n, e}
	}()

	vr := <-versionCh
	if vr.err != nil {
		errStr := vr.err.Error()
		if strings.Contains(strings.ToLower(errStr), "sso session") {
			status.Error = "AWS SSO session expired or invalid"
		} else {
			status.Error = fmt.Sprintf("Failed to get server version: %v", vr.err)
		}
		status.ResponseTimeMs = time.Since(startTime).Milliseconds()
		return status, nil
	}
	status.Version = vr.info.Major + "." + vr.info.Minor
	status.GitVersion = vr.info.GitVersion
	status.Platform = vr.info.Platform

	nr := <-nodesCh
	if nr.err != nil {
		log.Printf("Failed to list nodes for %s: %v", cluster, nr.err)
		status.Error = fmt.Sprintf("Failed to list nodes: %v", nr.err)
		status.ResponseTimeMs = time.Since(startTime).Milliseconds()
		return status, nil
	}
	status.NodeCount = nr.count

	nsr := <-nsCh
	if nsr.err != nil {
		log.Printf("Failed to list namespaces for %s: %v", cluster, nsr.err)
		status.Error = fmt.Sprintf("Failed to list namespaces: %v", nsr.err)
		status.ResponseTimeMs = time.Since(startTime).Milliseconds()
		return status, nil
	}
	status.NamespaceCount = nsr.count

	status.Healthy = true
	status.ResponseTimeMs = time.Since(startTime).Milliseconds()
	return status, nil
}

func (c *Client) ListNamespaces(cluster string) ([]string, error) {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	result := make([]string, len(namespaces.Items))
	for i, ns := range namespaces.Items {
		result[i] = ns.Name
	}

	return result, nil
}

func (c *Client) ScaleResource(ctx context.Context, cluster, group, version, kind, namespace, name string, replicas int32) error {
	dynamicClient, err := c.GetDynamicClient(cluster)
	if err != nil {
		return fmt.Errorf("failed to get dynamic client: %w", err)
	}

	// Get the correct resource name from discovery API
	resourceName := c.GetResourceName(cluster, group, version, kind)
	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resourceName,
	}

	var resource *unstructured.Unstructured
	if namespace != "" {
		resource, err = dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		resource, err = dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to get resource: %w", err)
	}

	spec, found, err := unstructured.NestedMap(resource.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("failed to get spec: %w", err)
	}

	err = unstructured.SetNestedField(spec, int64(replicas), "replicas")
	if err != nil {
		return fmt.Errorf("failed to set replicas: %w", err)
	}

	err = unstructured.SetNestedMap(resource.Object, spec, "spec")
	if err != nil {
		return fmt.Errorf("failed to update spec: %w", err)
	}

	if namespace != "" {
		_, err = dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, resource, metav1.UpdateOptions{})
	} else {
		_, err = dynamicClient.Resource(gvr).Update(ctx, resource, metav1.UpdateOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to update resource: %w", err)
	}

	return nil
}

func (c *Client) TaintNode(ctx context.Context, cluster, nodeName string, key, value, effect string) error {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return fmt.Errorf("failed to get client: %w", err)
	}

	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	taint := corev1.Taint{
		Key:    key,
		Value:  value,
		Effect: corev1.TaintEffect(effect),
	}

	taintExists := false
	for i, existingTaint := range node.Spec.Taints {
		if existingTaint.Key == key {
			node.Spec.Taints[i] = taint
			taintExists = true
			break
		}
	}

	if !taintExists {
		node.Spec.Taints = append(node.Spec.Taints, taint)
	}

	_, err = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	return nil
}

func (c *Client) RemoveTaint(ctx context.Context, cluster, nodeName, key string) error {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return fmt.Errorf("failed to get client: %w", err)
	}

	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	newTaints := []corev1.Taint{}
	for _, taint := range node.Spec.Taints {
		if taint.Key != key {
			newTaints = append(newTaints, taint)
		}
	}

	node.Spec.Taints = newTaints

	_, err = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	return nil
}

type DrainResult struct {
	DeletedPods []PodInfo `json:"deletedPods"`
	SkippedPods []PodInfo `json:"skippedPods"`
	FailedPods  []PodInfo `json:"failedPods"`
}

type PodInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Reason    string `json:"reason,omitempty"`
}

func (c *Client) DrainNode(ctx context.Context, cluster, nodeName string, ignoreDaemonsets, deleteEmptyDir bool, gracePeriod int) (*DrainResult, error) {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get client: %w", err)
	}

	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	node.Spec.Unschedulable = true
	_, err = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to cordon node: %w", err)
	}

	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	result := &DrainResult{
		DeletedPods: []PodInfo{},
		SkippedPods: []PodInfo{},
		FailedPods:  []PodInfo{},
	}

	gracePeriodSeconds := int64(gracePeriod)
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
	}

	for _, pod := range pods.Items {
		podInfo := PodInfo{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		}

		if pod.Namespace == "kube-system" {
			podInfo.Reason = "kube-system namespace"
			result.SkippedPods = append(result.SkippedPods, podInfo)
			continue
		}

		if ignoreDaemonsets {
			isDaemonSet := false
			for _, owner := range pod.OwnerReferences {
				if owner.Kind == "DaemonSet" {
					isDaemonSet = true
					break
				}
			}
			if isDaemonSet {
				podInfo.Reason = "DaemonSet pod"
				result.SkippedPods = append(result.SkippedPods, podInfo)
				continue
			}
		}

		if !deleteEmptyDir {
			hasEmptyDir := false
			for _, volume := range pod.Spec.Volumes {
				if volume.EmptyDir != nil {
					hasEmptyDir = true
					break
				}
			}
			if hasEmptyDir {
				podInfo.Reason = "has emptyDir volume"
				result.SkippedPods = append(result.SkippedPods, podInfo)
				continue
			}
		}

		err = client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, deleteOptions)
		if err != nil {
			podInfo.Reason = err.Error()
			result.FailedPods = append(result.FailedPods, podInfo)
			log.Printf("Failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
		} else {
			result.DeletedPods = append(result.DeletedPods, podInfo)
		}
	}

	return result, nil
}

func (c *Client) CordonNode(ctx context.Context, cluster, nodeName string, unschedulable bool) error {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return fmt.Errorf("failed to get client: %w", err)
	}

	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	node.Spec.Unschedulable = unschedulable

	_, err = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	return nil
}

func (c *Client) RestartResource(ctx context.Context, cluster, group, version, kind, namespace, name string) error {
	dynamicClient, err := c.GetDynamicClient(cluster)
	if err != nil {
		return fmt.Errorf("failed to get dynamic client: %w", err)
	}

	// Get the correct resource name from discovery API
	resourceName := c.GetResourceName(cluster, group, version, kind)
	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resourceName,
	}

	// Get the current resource
	var resource *unstructured.Unstructured
	if namespace != "" {
		resource, err = dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		resource, err = dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to get resource: %w", err)
	}

	// Update the kubectl.kubernetes.io/restartedAt annotation to trigger a restart
	annotations := resource.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Set the restart annotation with current timestamp
	annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	// For Deployments, StatefulSets, and DaemonSets, we need to update the template annotations
	spec, found, err := unstructured.NestedMap(resource.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("failed to get spec: %w", err)
	}

	template, found, err := unstructured.NestedMap(spec, "template")
	if err != nil || !found {
		return fmt.Errorf("failed to get template: %w", err)
	}

	templateMetadata, found, err := unstructured.NestedMap(template, "metadata")
	if err != nil || !found {
		templateMetadata = make(map[string]interface{})
	}

	templateAnnotations, found, err := unstructured.NestedStringMap(templateMetadata, "annotations")
	if err != nil || !found {
		templateAnnotations = make(map[string]string)
	}

	templateAnnotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	if err := unstructured.SetNestedStringMap(templateMetadata, templateAnnotations, "annotations"); err != nil {
		return fmt.Errorf("failed to set template annotations: %w", err)
	}

	if err := unstructured.SetNestedMap(template, templateMetadata, "metadata"); err != nil {
		return fmt.Errorf("failed to set template metadata: %w", err)
	}

	if err := unstructured.SetNestedMap(spec, template, "template"); err != nil {
		return fmt.Errorf("failed to set template: %w", err)
	}

	if err := unstructured.SetNestedMap(resource.Object, spec, "spec"); err != nil {
		return fmt.Errorf("failed to set spec: %w", err)
	}

	// Update the resource
	if namespace != "" {
		_, err = dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, resource, metav1.UpdateOptions{})
	} else {
		_, err = dynamicClient.Resource(gvr).Update(ctx, resource, metav1.UpdateOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to update resource: %w", err)
	}

	return nil
}

func (c *Client) TriggerCronJob(ctx context.Context, cluster, namespace, name string) (string, error) {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return "", fmt.Errorf("failed to get client: %w", err)
	}

	cronJob, err := client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get cronjob: %w", err)
	}

	jobName := fmt.Sprintf("%s-manual-%d", name, time.Now().Unix())
	if len(jobName) > 63 {
		jobName = jobName[:63]
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   namespace,
			Annotations: map[string]string{"cronjob.kubernetes.io/instantiate": "manual"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "CronJob",
				Name:       cronJob.Name,
				UID:        cronJob.UID,
			}},
		},
		Spec: cronJob.Spec.JobTemplate.Spec,
	}

	createdJob, err := client.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create job: %w", err)
	}

	return createdJob.Name, nil
}

func (c *Client) GetRolloutStatus(ctx context.Context, cluster, group, version, kind, namespace, name string) (*RolloutStatus, error) {
	// Only support rollout status for specific resource types
	supportedKinds := map[string]bool{
		"deployments":  true,
		"statefulsets": true,
		"daemonsets":   true,
		"replicasets":  true,
	}

	kindLower := strings.ToLower(kind)
	if !supportedKinds[kindLower] {
		return nil, fmt.Errorf("rollout status not supported for resource kind: %s", kind)
	}

	dynamicClient, err := c.GetDynamicClient(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic client: %w", err)
	}

	// Get the correct resource name from discovery API
	resourceName := c.GetResourceName(cluster, group, version, kind)
	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resourceName,
	}

	var resource *unstructured.Unstructured
	if namespace != "" {
		resource, err = dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		resource, err = dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get resource: %w", err)
	}

	status := &RolloutStatus{
		Status:  "Unknown",
		Message: "",
	}

	statusMap, found, _ := unstructured.NestedMap(resource.Object, "status")
	if !found {
		status.Status = "No status available"
		return status, nil
	}

	if replicas, ok := statusMap["replicas"].(int64); ok {
		status.Replicas = int32(replicas)
	}
	if updatedReplicas, ok := statusMap["updatedReplicas"].(int64); ok {
		status.UpdatedReplicas = int32(updatedReplicas)
	}
	if readyReplicas, ok := statusMap["readyReplicas"].(int64); ok {
		status.ReadyReplicas = int32(readyReplicas)
	}
	if availableReplicas, ok := statusMap["availableReplicas"].(int64); ok {
		status.AvailableReplicas = int32(availableReplicas)
	}
	if observedGen, ok := statusMap["observedGeneration"].(int64); ok {
		status.ObservedGeneration = observedGen
	}

	if conditions, ok := statusMap["conditions"].([]interface{}); ok {
		conditionsSlice := make([]map[string]interface{}, 0)
		for _, cond := range conditions {
			if condMap, ok := cond.(map[string]interface{}); ok {
				conditionsSlice = append(conditionsSlice, condMap)
			}
		}
		status.Conditions = conditionsSlice
	}

	// Determine rollout status based on replicas
	if status.Replicas == 0 {
		status.Status = "Scaled to 0"
		status.Message = "No replicas configured"
	} else if status.UpdatedReplicas < status.Replicas {
		status.Status = "Updating"
		status.Message = fmt.Sprintf("Rolling out new version (%d/%d updated)", status.UpdatedReplicas, status.Replicas)
	} else if status.ReadyReplicas < status.Replicas {
		status.Status = "Progressing"
		status.Message = fmt.Sprintf("Waiting for pods to be ready (%d/%d ready)", status.ReadyReplicas, status.Replicas)
	} else if status.AvailableReplicas < status.Replicas {
		status.Status = "Progressing"
		status.Message = fmt.Sprintf("Waiting for pods to be available (%d/%d available)", status.AvailableReplicas, status.Replicas)
	} else {
		status.Status = "Complete"
		status.Message = fmt.Sprintf("All %d replicas are running and ready", status.Replicas)

		// Check for any progressing conditions
		for _, cond := range status.Conditions {
			if condType, ok := cond["type"].(string); ok && condType == "Progressing" {
				if condStatus, ok := cond["status"].(string); ok && condStatus == "True" {
					if reason, ok := cond["reason"].(string); ok && reason == "NewReplicaSetAvailable" {
						status.Status = "Complete"
						status.Message = "Rollout completed successfully"
					}
				}
			}
		}
	}

	return status, nil
}

func (c *Client) GetBatchRolloutStatus(ctx context.Context, cluster string, requests []RolloutStatusRequest) []BatchRolloutStatus {
	results := make([]BatchRolloutStatus, len(requests))
	var wg sync.WaitGroup

	// Limit concurrent requests to avoid overwhelming the API server
	semaphore := make(chan struct{}, 5)

	for i, req := range requests {
		wg.Add(1)
		go func(idx int, r RolloutStatusRequest) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			key := fmt.Sprintf("%s/%s/%s/%s/%s", r.Group, r.Version, r.Kind, r.Namespace, r.Name)

			status, err := c.GetRolloutStatus(ctx, cluster, r.Group, r.Version, r.Kind, r.Namespace, r.Name)
			if err != nil {
				results[idx] = BatchRolloutStatus{
					Key:   key,
					Error: err.Error(),
				}
			} else {
				results[idx] = BatchRolloutStatus{
					Key:    key,
					Status: status,
				}
			}
		}(i, req)
	}

	wg.Wait()
	return results
}

func (c *Client) GetResourceCount(ctx context.Context, cluster string, gvr schema.GroupVersionResource) (int, error) {
	metadataClient, err := c.GetMetadataClient(cluster)
	if err != nil {
		return 0, err
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := metadataClient.Resource(gvr).List(timeoutCtx, metav1.ListOptions{Limit: 1, ResourceVersion: "0"})
	if err != nil {
		return 0, fmt.Errorf("failed to count resources: %w", err)
	}
	count := len(list.Items)
	if list.ListMeta.RemainingItemCount != nil {
		count += int(*list.ListMeta.RemainingItemCount)
	} else if count > 0 && list.ListMeta.Continue != "" {
		full, err := metadataClient.Resource(gvr).List(timeoutCtx, metav1.ListOptions{ResourceVersion: "0"})
		if err != nil {
			return 0, fmt.Errorf("failed to count resources: %w", err)
		}
		count = len(full.Items)
	}
	return count, nil
}

func (c *Client) UpdateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	dynamicClient, err := c.GetDynamicClient(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic client for cluster %s: %w", cluster, err)
	}

	var resourceInterface dynamic.ResourceInterface
	if namespace != "" {
		resourceInterface = dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceInterface = dynamicClient.Resource(gvr)
	}

	result, err := resourceInterface.Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			created, cerr := resourceInterface.Create(ctx, obj, metav1.CreateOptions{})
			if cerr != nil {
				return nil, fmt.Errorf("failed to create resource: %w", cerr)
			}
			return created, nil
		}
		return nil, fmt.Errorf("failed to update resource: %w", err)
	}

	return result, nil
}

func (c *Client) CreateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	dynamicClient, err := c.GetDynamicClient(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic client for cluster %s: %w", cluster, err)
	}

	var resourceInterface dynamic.ResourceInterface
	if namespace != "" {
		resourceInterface = dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceInterface = dynamicClient.Resource(gvr)
	}

	result, err := resourceInterface.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	return result, nil
}

func (c *Client) CreatePortForward(cluster, namespace, podName string, remotePort int) (*PortForward, error) {
	return c.portForwardManager.CreatePortForward(cluster, namespace, podName, remotePort)
}

func (c *Client) StopPortForward(id string) error {
	return c.portForwardManager.StopPortForward(id)
}

func (c *Client) GetPortForward(id string) (*PortForward, bool) {
	return c.portForwardManager.GetPortForward(id)
}

func (c *Client) GetPodLogs(ctx context.Context, cluster, namespace, name, container, tailLines string) (string, error) {
	client, err := c.GetClientForCluster(cluster)
	if err != nil {
		return "", fmt.Errorf("failed to get client for cluster %s: %w", cluster, err)
	}

	if container == "" {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get pod: %w", err)
		}

		if len(pod.Spec.Containers) > 0 {
			container = pod.Spec.Containers[0].Name
		}
	}

	opts := &corev1.PodLogOptions{}

	if container != "" {
		opts.Container = container
	}

	if tailLines != "" {
		lines, err := strconv.ParseInt(tailLines, 10, 64)
		if err == nil && lines > 0 {
			opts.TailLines = &lines
		}
	} else {
		defaultLines := int64(1000)
		opts.TailLines = &defaultLines
	}

	req := client.CoreV1().Pods(namespace).GetLogs(name, opts)

	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get pod logs: %w", err)
	}
	defer func() {
		if err := podLogs.Close(); err != nil {
			// Log error but don't return it since we're in a defer
			fmt.Printf("failed to close pod logs: %v\n", err)
		}
	}()

	buf, err := io.ReadAll(podLogs)
	if err != nil {
		return "", fmt.Errorf("failed to read pod logs: %w", err)
	}

	return string(buf), nil
}

func (c *Client) GetCrossplaneXRDs(cluster string) ([]map[string]interface{}, error) {
	dynamicClient, err := c.GetDynamicClient(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic client: %w", err)
	}

	// Define the GVR for CompositeResourceDefinitions
	xrdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.crossplane.io",
		Version:  "v1",
		Resource: "compositeresourcedefinitions",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// List all CompositeResourceDefinitions
	xrdList, err := dynamicClient.Resource(xrdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		// If Crossplane is not installed, return empty list
		if strings.Contains(err.Error(), "the server could not find the requested resource") {
			return []map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("failed to list XRDs: %w", err)
	}

	var xrds []map[string]interface{}
	for _, item := range xrdList.Items {
		spec, found, err := unstructured.NestedMap(item.Object, "spec")
		if err != nil || !found {
			continue
		}

		group, _, _ := unstructured.NestedString(spec, "group")
		names, _, _ := unstructured.NestedMap(spec, "names")
		kind, _, _ := unstructured.NestedString(names, "kind")
		plural, _, _ := unstructured.NestedString(names, "plural")

		// Get versions
		versions, _, _ := unstructured.NestedSlice(spec, "versions")
		for _, v := range versions {
			if versionMap, ok := v.(map[string]interface{}); ok {
				versionName, _, _ := unstructured.NestedString(versionMap, "name")
				if group != "" && kind != "" && plural != "" && versionName != "" {
					xrds = append(xrds, map[string]interface{}{
						"group":      group,
						"version":    versionName,
						"kind":       kind,
						"plural":     plural,
						"namespaced": true, // Crossplane claims are typically namespaced
					})
				}
			}
		}
	}

	return xrds, nil
}

func (c *Client) IsCrossplaneResource(cluster string, group string, version string, kind string) (bool, error) {
	// First check if this is a Crossplane internal resource
	crossplaneGroups := []string{
		"apiextensions.crossplane.io",
		"pkg.crossplane.io",
		"secrets.crossplane.io",
	}

	for _, cpGroup := range crossplaneGroups {
		if group == cpGroup {
			return true, nil
		}
	}

	// Now check if this resource is defined by a CompositeResourceDefinition
	xrds, err := c.GetCrossplaneXRDs(cluster)
	if err != nil {
		// If we can't get XRDs, assume it's not a Crossplane resource
		return false, nil
	}

	// Check if this resource matches any XRD
	for _, xrd := range xrds {
		xrdGroup, _ := xrd["group"].(string)
		xrdVersion, _ := xrd["version"].(string)
		xrdPlural, _ := xrd["plural"].(string)

		if xrdGroup == group && xrdVersion == version && xrdPlural == kind {
			return true, nil
		}
	}

	// Also check if this is a managed resource (has crossplane annotations/labels)
	dynamicClient, err := c.GetDynamicClient(cluster)
	if err != nil {
		return false, nil
	}

	// Check if the CRD has Crossplane annotations
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build CRD name from the resource - CRDs use plural form
	// Try to pluralize the kind (simple approach - add 's' if not already plural)
	plural := strings.ToLower(kind)
	if !strings.HasSuffix(plural, "s") {
		plural = plural + "s"
	}
	crdName := fmt.Sprintf("%s.%s", plural, group)

	crd, err := dynamicClient.Resource(crdGVR).Get(ctx, crdName, metav1.GetOptions{})
	if err != nil {
		return false, nil
	}

	// Check if CRD has Crossplane labels or annotations
	labels := crd.GetLabels()
	if labels != nil {
		if _, ok := labels["crossplane.io/managed"]; ok {
			return true, nil
		}
	}

	annotations := crd.GetAnnotations()
	for key := range annotations {
		if strings.Contains(key, "crossplane.io") {
			return true, nil
		}
	}

	return false, nil
}
