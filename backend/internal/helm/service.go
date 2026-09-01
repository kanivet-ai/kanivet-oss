package helm

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Service struct {
	k8sClient     k8s.Interface
	settings      *cli.EnvSettings
	mu            sync.RWMutex
	// ActionConfig cache per cluster for faster subsequent requests
	configCache   map[string]*action.Configuration
	configCacheMu sync.RWMutex
}

type Release struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Revision     int               `json:"revision"`
	Status       string            `json:"status"`
	Chart        string            `json:"chart"`
	ChartVersion string            `json:"chartVersion"`
	AppVersion   string            `json:"appVersion"`
	Updated      time.Time         `json:"updated"`
	Description  string            `json:"description"`
	Notes        string            `json:"notes,omitempty"`
	Values       map[string]any    `json:"values,omitempty"`
	Manifest     string            `json:"manifest,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

type ReleaseDetail struct {
	Release
	ChartMetadata *ChartMetadata   `json:"chartMetadata,omitempty"`
	Hooks         []Hook           `json:"hooks,omitempty"`
	Resources     []ManagedResource `json:"resources,omitempty"`
}

type ChartMetadata struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	AppVersion  string   `json:"appVersion"`
	Description string   `json:"description"`
	Home        string   `json:"home,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Maintainers []string `json:"maintainers,omitempty"`
}

type Hook struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Path           string   `json:"path"`
	Events         []string `json:"events"`
	Weight         int      `json:"weight"`
	DeletePolicies []string `json:"deletePolicies,omitempty"`
}

type ManagedResource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type HistoryEntry struct {
	Revision    int       `json:"revision"`
	Status      string    `json:"status"`
	Chart       string    `json:"chart"`
	AppVersion  string    `json:"appVersion"`
	Updated     time.Time `json:"updated"`
	Description string    `json:"description"`
}

func NewService(k8sClient k8s.Interface) *Service {
	return &Service{
		k8sClient:   k8sClient,
		settings:    cli.New(),
		configCache: make(map[string]*action.Configuration),
	}
}

func (s *Service) getActionConfig(cluster, namespace string) (*action.Configuration, error) {
	// Cache key includes cluster and namespace
	cacheKey := cluster + ":" + namespace
	
	// Check cache first
	s.configCacheMu.RLock()
	if cfg, ok := s.configCache[cacheKey]; ok {
		s.configCacheMu.RUnlock()
		return cfg, nil
	}
	s.configCacheMu.RUnlock()
	
	// Create new config
	_, restConfig, err := s.k8sClient.GetClientAndConfig(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s config for cluster %s: %w", cluster, err)
	}

	restClientGetter := &SimpleRESTClientGetter{
		RestConfig: restConfig,
		Namespace:  namespace,
	}

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(restClientGetter, namespace, "secret", log.Printf); err != nil {
		return nil, fmt.Errorf("failed to initialize helm action config: %w", err)
	}

	// Cache for future use
	s.configCacheMu.Lock()
	s.configCache[cacheKey] = actionConfig
	s.configCacheMu.Unlock()

	return actionConfig, nil
}

// InvalidateConfigCache clears the ActionConfig cache for a cluster (call on cluster disconnect)
func (s *Service) InvalidateConfigCache(cluster string) {
	s.configCacheMu.Lock()
	defer s.configCacheMu.Unlock()
	
	for key := range s.configCache {
		if strings.HasPrefix(key, cluster+":") {
			delete(s.configCache, key)
		}
	}
}

func (s *Service) ListReleases(ctx context.Context, cluster string, namespace string, allNamespaces bool) ([]Release, error) {
	log.Printf("[Helm] ListReleases called: cluster=%s, namespace=%s, allNamespaces=%v", cluster, namespace, allNamespaces)
	s.mu.RLock()
	defer s.mu.RUnlock()

	if allNamespaces {
		return s.listReleasesAllNamespacesParallel(ctx, cluster)
	}

	return s.listReleasesInNamespace(ctx, cluster, namespace)
}

// listReleasesInNamespace fetches releases from a single namespace (fast)
func (s *Service) listReleasesInNamespace(ctx context.Context, cluster, namespace string) ([]Release, error) {
	startConfig := time.Now()
	actionConfig, err := s.getActionConfig(cluster, namespace)
	configDur := time.Since(startConfig)
	if err != nil {
		return nil, err
	}

	listAction := action.NewList(actionConfig)
	// Only list deployed releases (not superseded/failed history) for much better performance
	listAction.Deployed = true
	listAction.Failed = true      // Include failed so user can see issues
	listAction.Pending = true     // Include pending installs/upgrades
	listAction.AllNamespaces = false

	startList := time.Now()
	releases, err := listAction.Run()
	listDur := time.Since(startList)
	
	// Log slow namespaces
	if configDur > 500*time.Millisecond || listDur > 500*time.Millisecond {
		log.Printf("[Helm] Slow namespace %s: config=%v, list=%v, releases=%d", 
			namespace, configDur, listDur, len(releases))
	}
	
	if err != nil {
		if err == driver.ErrReleaseNotFound {
			return []Release{}, nil
		}
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	result := make([]Release, 0, len(releases))
	for _, rel := range releases {
		result = append(result, s.convertRelease(rel))
	}

	return result, nil
}

// listReleasesAllNamespacesParallel fetches releases from all namespaces in parallel
// This is typically 3-5x faster than a single allNamespaces query
func (s *Service) listReleasesAllNamespacesParallel(ctx context.Context, cluster string) ([]Release, error) {
	startTotal := time.Now()
	
	// Get list of namespaces
	startNsList := time.Now()
	client, _, err := s.k8sClient.GetClientAndConfig(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s client: %w", err)
	}

	namespaceList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		// Fallback to single query if namespace list fails
		log.Printf("[Helm] Failed to list namespaces, falling back to single query: %v", err)
		return s.listReleasesAllNamespacesFallback(ctx, cluster)
	}
	log.Printf("[Helm] Listed %d namespaces in %v", len(namespaceList.Items), time.Since(startNsList))

	namespaces := make([]string, 0, len(namespaceList.Items))
	for _, ns := range namespaceList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	if len(namespaces) == 0 {
		return []Release{}, nil
	}

	// Limit concurrency to avoid overwhelming the API server
	const maxConcurrency = 20 // Increased from 10
	semaphore := make(chan struct{}, maxConcurrency)
	
	type namespaceResult struct {
		namespace string
		releases  []Release
		err       error
		duration  time.Duration
	}
	
	results := make(chan namespaceResult, len(namespaces))
	var wg sync.WaitGroup

	startParallel := time.Now()
	for _, ns := range namespaces {
		wg.Add(1)
		go func(namespace string) {
			defer wg.Done()
			
			// Acquire semaphore
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- namespaceResult{namespace: namespace, err: ctx.Err()}
				return
			}

			nsStart := time.Now()
			releases, err := s.listReleasesInNamespace(ctx, cluster, namespace)
			results <- namespaceResult{
				namespace: namespace,
				releases:  releases,
				err:       err,
				duration:  time.Since(nsStart),
			}
		}(ns)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var allReleases []Release
	var firstErr error
	var slowest time.Duration
	var slowestNs string
	nsWithReleases := 0
	
	for result := range results {
		if result.duration > slowest {
			slowest = result.duration
			slowestNs = result.namespace
		}
		if result.err != nil {
			// Log but continue - don't fail entire request for one namespace
			if firstErr == nil && result.err != driver.ErrReleaseNotFound {
				log.Printf("[Helm] Error fetching releases from namespace %s: %v", result.namespace, result.err)
			}
			continue
		}
		if len(result.releases) > 0 {
			nsWithReleases++
			allReleases = append(allReleases, result.releases...)
		}
	}
	
	log.Printf("[Helm] Parallel fetch: %d namespaces, %d with releases, %d total releases, slowest: %s (%v), parallel time: %v, total: %v",
		len(namespaces), nsWithReleases, len(allReleases), slowestNs, slowest, time.Since(startParallel), time.Since(startTotal))

	// Sort results
	sort.Slice(allReleases, func(i, j int) bool {
		if allReleases[i].Namespace != allReleases[j].Namespace {
			return allReleases[i].Namespace < allReleases[j].Namespace
		}
		return allReleases[i].Name < allReleases[j].Name
	})

	return allReleases, firstErr
}

// listReleasesAllNamespacesFallback uses the original single-query approach
func (s *Service) listReleasesAllNamespacesFallback(ctx context.Context, cluster string) ([]Release, error) {
	actionConfig, err := s.getActionConfig(cluster, "")
	if err != nil {
		return nil, err
	}

	listAction := action.NewList(actionConfig)
	listAction.All = true
	listAction.AllNamespaces = true

	releases, err := listAction.Run()
	if err != nil {
		if err == driver.ErrReleaseNotFound {
			return []Release{}, nil
		}
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	result := make([]Release, 0, len(releases))
	for _, rel := range releases {
		result = append(result, s.convertRelease(rel))
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Namespace != result[j].Namespace {
			return result[i].Namespace < result[j].Namespace
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// StreamProgress represents the loading progress for streaming releases
type StreamProgress struct {
	Total     int
	Completed int
}

// StreamReleases streams releases as each namespace completes, providing immediate feedback
// Returns two channels: releases (batches of releases) and progress updates
func (s *Service) StreamReleases(ctx context.Context, cluster string) (<-chan []Release, <-chan StreamProgress, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get list of namespaces
	client, _, err := s.k8sClient.GetClientAndConfig(cluster)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get k8s client: %w", err)
	}

	namespaceList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	namespaces := make([]string, 0, len(namespaceList.Items))
	for _, ns := range namespaceList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	releasesChan := make(chan []Release, 50)
	progressChan := make(chan StreamProgress, len(namespaces)+1)

	// Send initial progress
	progressChan <- StreamProgress{Total: len(namespaces), Completed: 0}

	go func() {
		defer close(releasesChan)
		defer close(progressChan)

		const maxConcurrency = 20
		semaphore := make(chan struct{}, maxConcurrency)

		type namespaceResult struct {
			namespace string
			releases  []Release
			err       error
		}

		results := make(chan namespaceResult, len(namespaces))
		var wg sync.WaitGroup

		// Launch all namespace fetches
		for _, ns := range namespaces {
			wg.Add(1)
			go func(namespace string) {
				defer wg.Done()

				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					results <- namespaceResult{namespace: namespace, err: ctx.Err()}
					return
				}

				releases, err := s.listReleasesInNamespace(ctx, cluster, namespace)
				results <- namespaceResult{
					namespace: namespace,
					releases:  releases,
					err:       err,
				}
			}(ns)
		}

		// Close results channel when all goroutines complete
		go func() {
			wg.Wait()
			close(results)
		}()

		// Stream results as they arrive
		completed := 0
		for result := range results {
			completed++

			// Send progress update
			select {
			case progressChan <- StreamProgress{Total: len(namespaces), Completed: completed}:
			default:
				// Don't block on progress
			}

			if result.err != nil {
				if result.err != driver.ErrReleaseNotFound {
					log.Printf("[Helm] Stream error for namespace %s: %v", result.namespace, result.err)
				}
				continue
			}

			// Only send if there are releases
			if len(result.releases) > 0 {
				select {
				case releasesChan <- result.releases:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return releasesChan, progressChan, nil
}

func (s *Service) GetRelease(ctx context.Context, cluster, namespace, name string) (*ReleaseDetail, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return nil, err
	}

	getAction := action.NewGet(actionConfig)
	rel, err := getAction.Run(name)
	if err != nil {
		if err == driver.ErrReleaseNotFound {
			return nil, fmt.Errorf("release %s not found in namespace %s", name, namespace)
		}
		return nil, fmt.Errorf("failed to get release: %w", err)
	}

	detail := &ReleaseDetail{
		Release: s.convertRelease(rel),
	}
	detail.Values = rel.Config
	detail.Manifest = rel.Manifest
	detail.Notes = rel.Info.Notes

	if rel.Chart != nil && rel.Chart.Metadata != nil {
		detail.ChartMetadata = s.convertChartMetadata(rel.Chart.Metadata)
	}

	detail.Hooks = s.convertHooks(rel.Hooks)
	detail.Resources = s.parseManifestResources(rel.Manifest)

	return detail, nil
}

// GetReleaseBasic returns a Release with basic info for list display
func (s *Service) GetReleaseBasic(ctx context.Context, cluster, namespace, name string) (*Release, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return nil, err
	}

	getAction := action.NewGet(actionConfig)
	rel, err := getAction.Run(name)
	if err != nil {
		if err == driver.ErrReleaseNotFound {
			return nil, fmt.Errorf("release %s not found in namespace %s", name, namespace)
		}
		return nil, fmt.Errorf("failed to get release: %w", err)
	}

	release := s.convertRelease(rel)
	return &release, nil
}

func (s *Service) GetReleaseValues(ctx context.Context, cluster, namespace, name string, allValues bool) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return nil, err
	}

	getValues := action.NewGetValues(actionConfig)
	getValues.AllValues = allValues

	values, err := getValues.Run(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get values: %w", err)
	}

	return values, nil
}

func (s *Service) GetReleaseManifest(ctx context.Context, cluster, namespace, name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return "", err
	}

	getAction := action.NewGet(actionConfig)
	rel, err := getAction.Run(name)
	if err != nil {
		return "", fmt.Errorf("failed to get release: %w", err)
	}

	return rel.Manifest, nil
}

func (s *Service) GetReleaseHistory(ctx context.Context, cluster, namespace, name string, max int) ([]HistoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return nil, err
	}

	historyAction := action.NewHistory(actionConfig)
	if max > 0 {
		historyAction.Max = max
	} else {
		historyAction.Max = 256
	}

	releases, err := historyAction.Run(name)
	if err != nil {
		if err == driver.ErrReleaseNotFound {
			return []HistoryEntry{}, nil
		}
		return nil, fmt.Errorf("failed to get release history: %w", err)
	}

	history := make([]HistoryEntry, 0, len(releases))
	for _, rel := range releases {
		entry := HistoryEntry{
			Revision:   rel.Version,
			Status:     rel.Info.Status.String(),
			Updated:    rel.Info.LastDeployed.Time,
			Description: rel.Info.Description,
		}
		if rel.Chart != nil && rel.Chart.Metadata != nil {
			entry.Chart = rel.Chart.Metadata.Name + "-" + rel.Chart.Metadata.Version
			entry.AppVersion = rel.Chart.Metadata.AppVersion
		}
		history = append(history, entry)
	}

	sort.Slice(history, func(i, j int) bool {
		return history[i].Revision > history[j].Revision
	})

	return history, nil
}

func (s *Service) RollbackRelease(ctx context.Context, cluster, namespace, name string, revision int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return err
	}

	rollbackAction := action.NewRollback(actionConfig)
	rollbackAction.Version = revision
	rollbackAction.Wait = true
	rollbackAction.Timeout = 5 * time.Minute

	if err := rollbackAction.Run(name); err != nil {
		return fmt.Errorf("failed to rollback release: %w", err)
	}

	return nil
}

func (s *Service) UninstallRelease(ctx context.Context, cluster, namespace, name string, keepHistory bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return err
	}

	uninstallAction := action.NewUninstall(actionConfig)
	uninstallAction.KeepHistory = keepHistory
	uninstallAction.Timeout = 5 * time.Minute

	_, err = uninstallAction.Run(name)
	if err != nil {
		return fmt.Errorf("failed to uninstall release: %w", err)
	}

	return nil
}

func (s *Service) UpgradeRelease(ctx context.Context, cluster, namespace, name string, chartPath string, values map[string]any, dryRun bool) (*Release, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return nil, "", err
	}

	upgradeAction := action.NewUpgrade(actionConfig)
	upgradeAction.Namespace = namespace
	upgradeAction.DryRun = dryRun
	upgradeAction.Wait = !dryRun
	upgradeAction.Timeout = 5 * time.Minute
	upgradeAction.ReuseValues = true

	chartReq, err := upgradeAction.ChartPathOptions.LocateChart(chartPath, s.settings)
	if err != nil {
		return nil, "", fmt.Errorf("failed to locate chart: %w", err)
	}

	chartLoaded, err := loadChart(chartReq)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load chart: %w", err)
	}

	rel, err := upgradeAction.Run(name, chartLoaded, values)
	if err != nil {
		return nil, "", fmt.Errorf("failed to upgrade release: %w", err)
	}

	result := s.convertRelease(rel)
	return &result, rel.Manifest, nil
}

func (s *Service) UpgradeReleaseValues(ctx context.Context, cluster, namespace, name string, values map[string]any, dryRun bool) (*Release, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	actionConfig, err := s.getActionConfig(cluster, namespace)
	if err != nil {
		return nil, "", err
	}

	getAction := action.NewGet(actionConfig)
	existingRelease, err := getAction.Run(name)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get existing release: %w", err)
	}

	if existingRelease.Chart == nil {
		return nil, "", fmt.Errorf("existing release has no chart")
	}

	upgradeAction := action.NewUpgrade(actionConfig)
	upgradeAction.Namespace = namespace
	upgradeAction.DryRun = dryRun
	upgradeAction.Wait = !dryRun
	upgradeAction.Timeout = 5 * time.Minute
	upgradeAction.ReuseValues = false
	upgradeAction.ResetValues = false

	rel, err := upgradeAction.Run(name, existingRelease.Chart, values)
	if err != nil {
		return nil, "", fmt.Errorf("failed to upgrade release: %w", err)
	}

	result := s.convertRelease(rel)
	return &result, rel.Manifest, nil
}

func (s *Service) convertRelease(rel *release.Release) Release {
	r := Release{
		Name:      rel.Name,
		Namespace: rel.Namespace,
		Revision:  rel.Version,
		Labels:    rel.Labels,
	}

	if rel.Info != nil {
		r.Status = rel.Info.Status.String()
		r.Updated = rel.Info.LastDeployed.Time
		r.Description = rel.Info.Description
	}

	if rel.Chart != nil && rel.Chart.Metadata != nil {
		r.Chart = rel.Chart.Metadata.Name
		r.ChartVersion = rel.Chart.Metadata.Version
		r.AppVersion = rel.Chart.Metadata.AppVersion
	}

	return r
}

func (s *Service) convertChartMetadata(meta *chart.Metadata) *ChartMetadata {
	if meta == nil {
		return nil
	}

	cm := &ChartMetadata{
		Name:        meta.Name,
		Version:     meta.Version,
		AppVersion:  meta.AppVersion,
		Description: meta.Description,
		Home:        meta.Home,
		Icon:        meta.Icon,
		Sources:     meta.Sources,
		Keywords:    meta.Keywords,
	}

	for _, m := range meta.Maintainers {
		if m != nil {
			cm.Maintainers = append(cm.Maintainers, m.Name)
		}
	}

	return cm
}

func (s *Service) convertHooks(hooks []*release.Hook) []Hook {
	result := make([]Hook, 0, len(hooks))
	for _, h := range hooks {
		if h == nil {
			continue
		}
		hook := Hook{
			Name:   h.Name,
			Kind:   h.Kind,
			Path:   h.Path,
			Weight: h.Weight,
		}
		for _, e := range h.Events {
			hook.Events = append(hook.Events, string(e))
		}
		for _, p := range h.DeletePolicies {
			hook.DeletePolicies = append(hook.DeletePolicies, string(p))
		}
		result = append(result, hook)
	}
	return result
}

func (s *Service) parseManifestResources(manifest string) []ManagedResource {
	resources := []ManagedResource{}
	docs := strings.Split(manifest, "---")
	
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		var kind, name, namespace string
		lines := strings.Split(doc, "\n")
		inMetadata := false
		
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			
			if strings.HasPrefix(trimmed, "kind:") {
				kind = strings.TrimSpace(strings.TrimPrefix(trimmed, "kind:"))
			} else if trimmed == "metadata:" {
				inMetadata = true
			} else if inMetadata {
				if strings.HasPrefix(trimmed, "name:") {
					name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
				} else if strings.HasPrefix(trimmed, "namespace:") {
					namespace = strings.TrimSpace(strings.TrimPrefix(trimmed, "namespace:"))
				} else if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
					inMetadata = false
				}
			}
		}

		if kind != "" && name != "" {
			resources = append(resources, ManagedResource{
				Kind:      kind,
				Name:      name,
				Namespace: namespace,
			})
		}
	}

	return resources
}

func loadChart(path string) (*chart.Chart, error) {
	return nil, fmt.Errorf("chart loading not implemented - use existing release charts")
}

// GetK8sClient returns the kubernetes client for a cluster
func (s *Service) GetK8sClient(cluster string) (kubernetes.Interface, *rest.Config, error) {
	return s.k8sClient.GetClientAndConfig(cluster)
}

// ParseReleaseFromSecret parses a Helm release from a Kubernetes secret
// Helm stores release data in secrets with owner=helm label
func (s *Service) ParseReleaseFromSecret(obj interface{}) (*Release, error) {
	// Type assert to corev1.Secret
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		// Try runtime.Object
		runtimeObj, ok := obj.(runtime.Object)
		if !ok {
			return nil, fmt.Errorf("object is not a secret")
		}
		// Try to get as unstructured
		unstr, ok := runtimeObj.(*unstructured.Unstructured)
		if !ok {
			return nil, fmt.Errorf("cannot convert to unstructured")
		}
		
		// Parse from unstructured
		labels := unstr.GetLabels()
		if labels["owner"] != "helm" {
			return nil, fmt.Errorf("not a helm release secret")
		}
		
		name := labels["name"]
		namespace := unstr.GetNamespace()
		status := labels["status"]
		
		// Parse version from label
		version := 0
		if v, ok := labels["version"]; ok {
			fmt.Sscanf(v, "%d", &version)
		}
		
		return &Release{
			Name:      name,
			Namespace: namespace,
			Revision:  version,
			Status:    status,
			Updated:   unstr.GetCreationTimestamp().Time,
		}, nil
	}
	
	// Parse from typed secret
	labels := secret.Labels
	if labels["owner"] != "helm" {
		return nil, fmt.Errorf("not a helm release secret")
	}
	
	name := labels["name"]
	namespace := secret.Namespace
	status := labels["status"]
	
	// Parse version from label
	version := 0
	if v, ok := labels["version"]; ok {
		fmt.Sscanf(v, "%d", &version)
	}
	
	// Try to decode the release data for more info
	// The release data is base64 + gzip compressed in the secret
	// For now, just return basic info from labels
	return &Release{
		Name:      name,
		Namespace: namespace,
		Revision:  version,
		Status:    status,
		Updated:   secret.CreationTimestamp.Time,
	}, nil
}
