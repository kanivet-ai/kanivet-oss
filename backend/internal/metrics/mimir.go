package metrics

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MimirProvider struct {
	k8s             k8s.Interface
	cache           *cache.Cache
	portForwardPool sync.Map
	// tenantLookup returns the X-Scope-OrgID to send for a given cluster.
	// Multi-tenant Mimir silently returns empty results when this header is
	// missing or wrong, so we treat the per-cluster value as a hard requirement
	// and let callers persist it via SetTenantLookup. Returns empty string if
	// no override has been configured (and the request is then sent without
	// the header — works only for single-tenant or auth-disabled Mimir).
	tenantLookup func(cluster string) string
	// serviceLookup returns the operator-chosen Mimir (namespace, service) for a
	// cluster, overriding auto-discovery. Empty service means "use auto-discovery".
	serviceLookup func(cluster string) (namespace, service string)
}

func NewMimirProvider(k8sClient k8s.Interface, cacheInstance *cache.Cache) *MimirProvider {
	p := &MimirProvider{
		k8s:   k8sClient,
		cache: cacheInstance,
	}
	go p.cleanupUnusedPortForwards()
	go p.keepAlivePortForwards()
	return p
}

// SetTenantLookup wires a per-cluster tenant resolver (typically backed by the
// SQLite settings table). Safe to call once at startup before any queries run.
func (p *MimirProvider) SetTenantLookup(fn func(cluster string) string) {
	p.tenantLookup = fn
}

// SetServiceLookup wires a per-cluster Mimir service resolver (backed by the
// SQLite settings table). Safe to call once at startup before any queries run.
func (p *MimirProvider) SetServiceLookup(fn func(cluster string) (namespace, service string)) {
	p.serviceLookup = fn
}

// DiscoverTenants returns the real tenant IDs Mimir holds data for. It first
// asks the distributor to enumerate its tenants via /distributor/all_user_stats
// (exposed through the gateway with no auth header) so opaque tenant names that
// aren't derivable from the cluster — e.g. "example-tenant" — are found without the
// user having to type them. Any caller-supplied hints and a few well-known
// defaults are merged in as a fallback for setups where that endpoint is
// blocked. Each candidate is then confirmed by sending GET
// /prometheus/api/v1/labels with it as X-Scope-OrgID and keeping those whose
// `data` array is non-empty — proof Mimir actually has data for that tenant.
func (p *MimirProvider) DiscoverTenants(cluster string, hints []string) ([]string, error) {
	info, err := p.Detect(cluster)
	if err != nil || !info.Found {
		return nil, fmt.Errorf("mimir not detected in cluster")
	}
	pfInfo, err := p.getOrCreatePortForward(cluster, info)
	if err != nil {
		return nil, fmt.Errorf("failed to get mimir port forward: %w", err)
	}

	candidates := dedupeStrings(append([]string{}, hints...))
	enumerated := p.enumerateTenants(pfInfo)
	for _, c := range enumerated {
		candidates = appendUnique(candidates, c)
	}
	// The guess-list is only a fallback for when the distributor won't enumerate
	// its tenants. When enumeration succeeded those IDs are authoritative, so
	// skip the guesses — otherwise generic values like "anonymous"/"0" that
	// happen to hold unrelated data drown out the real tenant.
	if len(enumerated) == 0 {
		for _, c := range []string{"anonymous", "0", cluster, lastSegment(cluster)} {
			if c != "" {
				candidates = appendUnique(candidates, c)
			}
		}
	}

	url := fmt.Sprintf("http://localhost:%d/prometheus/api/v1/labels", pfInfo.PortForward.LocalPort)
	var viable []string
	for _, tenant := range candidates {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("X-Scope-OrgID", tenant)
		resp, err := pfInfo.HTTPClient.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var probe struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}
		if sonic.Unmarshal(body, &probe) == nil && probe.Status == "success" && len(probe.Data) > 0 {
			viable = append(viable, tenant)
		}
	}
	return viable, nil
}

// enumerateTenants asks the distributor for the tenant IDs it knows about. The
// endpoint returns JSON when Accept: application/json is sent. Returns nil on
// any failure (endpoint blocked, non-200, parse error) so callers fall back to
// the guess-list — discovery degrades, it doesn't break.
func (p *MimirProvider) enumerateTenants(pfInfo *PortForwardInfo) []string {
	url := fmt.Sprintf("http://localhost:%d/distributor/all_user_stats", pfInfo.PortForward.LocalPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	resp, err := pfInfo.HTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return parseUserStatsTenants(body)
}

// parseUserStatsTenants extracts tenant IDs from a Mimir
// /distributor/all_user_stats JSON response: [{"userID":"example-tenant",...}, ...].
func parseUserStatsTenants(body []byte) []string {
	var stats []struct {
		UserID string `json:"userID"`
	}
	if sonic.Unmarshal(body, &stats) != nil {
		return nil
	}
	tenants := make([]string, 0, len(stats))
	for _, s := range stats {
		if s.UserID != "" {
			tenants = appendUnique(tenants, s.UserID)
		}
	}
	return tenants
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func appendUnique(slice []string, s string) []string {
	for _, existing := range slice {
		if existing == s {
			return slice
		}
	}
	return append(slice, s)
}

// lastSegment returns the part of an EKS-style ARN after the last "/" — for
// "arn:aws:eks:eu-north-1:example-account:cluster/example-cluster" it returns
// "example-cluster", which is the form most operators use as
// their Mimir tenant ID.
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return s
}

// doMimirRequest performs a GET against the cluster's Mimir port-forward,
// applying the configured X-Scope-OrgID tenant header when one is set. All
// Mimir HTTP calls in this package go through here so adding the header is
// a single place to maintain.
func (p *MimirProvider) doMimirRequest(cluster string, pfInfo *PortForwardInfo, fullURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	if p.tenantLookup != nil {
		if tenant := p.tenantLookup(cluster); tenant != "" {
			req.Header.Set("X-Scope-OrgID", tenant)
		}
	}
	return pfInfo.HTTPClient.Do(req)
}

func (p *MimirProvider) getOrCreatePortForward(cluster string, info *ProviderInfo) (*PortForwardInfo, error) {
	cacheKey := "mimir-" + cluster + "-" + info.Namespace + "-" + info.Service
	if pfi, ok := p.portForwardPool.Load(cacheKey); ok {
		pfInfo := pfi.(*PortForwardInfo)
		pfInfo.Mutex.Lock()
		pfInfo.LastUsed = time.Now()
		pfInfo.Mutex.Unlock()
		return pfInfo, nil
	}

	targetPort := info.Port
	if targetPort == 0 {
		targetPort = 8080 // Default for mimir components
	}
	podName := p.findMimirPod(cluster, info.Namespace, info.Service)
	if podName == "" {
		return nil, fmt.Errorf("no mimir pod found")
	}
	pf, err := p.k8s.CreatePortForward(cluster, info.Namespace, podName, int(targetPort))
	if err != nil {
		return nil, fmt.Errorf("failed to create port forward: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
			DisableKeepAlives:   false,
			DialContext: (&net.Dialer{
				Timeout:   1 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	pfInfo := &PortForwardInfo{
		PortForward: pf,
		HTTPClient:  httpClient,
		LastUsed:    time.Now(),
	}
	p.portForwardPool.Store(cacheKey, pfInfo)
	time.Sleep(100 * time.Millisecond)
	return pfInfo, nil
}

func (p *MimirProvider) findMimirPod(cluster, namespace, serviceName string) string {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return ""
	}

	var labelSelector string
	for k, v := range svc.Spec.Selector {
		if labelSelector != "" {
			labelSelector += ","
		}
		labelSelector += fmt.Sprintf("%s=%s", k, v)
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
		Limit:         5,
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == v1.PodRunning {
			return pod.Name
		}
	}
	return ""
}

func (p *MimirProvider) cleanupUnusedPortForwards() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		p.portForwardPool.Range(func(key, value interface{}) bool {
			pfInfo := value.(*PortForwardInfo)
			pfInfo.Mutex.Lock()
			if time.Since(pfInfo.LastUsed) > 10*time.Minute {
				_ = p.k8s.StopPortForward(pfInfo.PortForward.ID)
				p.portForwardPool.Delete(key)
			}
			pfInfo.Mutex.Unlock()
			return true
		})
	}
}

func (p *MimirProvider) keepAlivePortForwards() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		p.portForwardPool.Range(func(key, value interface{}) bool {
			pfInfo := value.(*PortForwardInfo)
			pfInfo.Mutex.Lock()
			resp, err := pfInfo.HTTPClient.Get(fmt.Sprintf("http://localhost:%d/prometheus/api/v1/status/buildinfo", pfInfo.PortForward.LocalPort))
			if err == nil {
				resp.Body.Close()
			}
			pfInfo.Mutex.Unlock()
			return true
		})
	}
}

func (p *MimirProvider) GetName() string {
	return "mimir"
}

func (p *MimirProvider) Detect(cluster string) (*ProviderInfo, error) {
	cacheKey := p.cache.BuildKey("mimir-info", cluster)
	data, err := p.cache.GetOrSet(cacheKey, 30*time.Minute, func() (interface{}, error) {
		return p.detectInternal(cluster)
	})
	if err != nil {
		return nil, err
	}
	info := data.(*ProviderInfo)
	// A user-chosen Mimir service overrides auto-discovery: clusters can expose
	// several Mimir gateways (e.g. a host-level one and vcluster-mapped copies)
	// and only one holds the container metrics we query, so the operator's pick
	// wins. We match the choice against the live candidate list to recover its
	// port/URL; if it's no longer present we fall back to auto-discovery.
	if p.serviceLookup != nil {
		if ns, svc := p.serviceLookup(cluster); svc != "" {
			if chosen := p.findCandidate(cluster, ns, svc); chosen != nil {
				return chosen, nil
			}
		}
	}
	return info, nil
}

// DetectAllMimirServices returns every Mimir gateway service found in the
// cluster, highest-priority first, so the UI can let the operator pick which
// one to query.
func (p *MimirProvider) DetectAllMimirServices(cluster string) ([]*ProviderInfo, error) {
	return p.mimirCandidates(cluster)
}

func (p *MimirProvider) findCandidate(cluster, namespace, service string) *ProviderInfo {
	candidates, err := p.mimirCandidates(cluster)
	if err != nil {
		return nil
	}
	for _, c := range candidates {
		if c.Service == service && (namespace == "" || c.Namespace == namespace) {
			return c
		}
	}
	return nil
}

func (p *MimirProvider) detectInternal(cluster string) (*ProviderInfo, error) {
	candidates, err := p.mimirCandidates(cluster)
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return &ProviderInfo{Type: "mimir", Found: false}, nil
}

func (p *MimirProvider) mimirCandidates(cluster string) ([]*ProviderInfo, error) {
	cacheKey := p.cache.BuildKey("mimir-candidates", cluster)
	cached, err := p.cache.GetOrSet(cacheKey, 60*time.Second, func() (any, error) {
		return p.mimirCandidatesUncached(cluster)
	})
	if err != nil {
		return nil, err
	}
	return cached.([]*ProviderInfo), nil
}

func (p *MimirProvider) mimirCandidatesUncached(cluster string) ([]*ProviderInfo, error) {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Single API call to get ALL services across all namespaces, served from
	// the apiserver watch cache instead of a quorum etcd read.
	services, err := clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	// Priority order: mimir-nginx > mimir-gateway > mimir-query-frontend > any mimir service
	mimirServicePatterns := []string{"mimir-nginx", "mimir-gateway", "mimir-query-frontend"}
	var candidates []*ProviderInfo

	for _, svc := range services.Items {
		svcNameLower := strings.ToLower(svc.Name)
		priority := -1
		for i, pattern := range mimirServicePatterns {
			if svcNameLower == pattern || strings.HasPrefix(svcNameLower, pattern) {
				priority = len(mimirServicePatterns) - i
				break
			}
		}
		if priority < 0 && strings.Contains(svcNameLower, "mimir") {
			priority = 0
		}
		if priority < 0 {
			continue
		}

		// Get the target port (container port) for port-forwarding
		var port int32
		for _, sp := range svc.Spec.Ports {
			if sp.Name == "http" || sp.Name == "http-metric" || sp.Port == 80 || sp.Port == 8080 {
				// Use TargetPort if it's a number, otherwise default to common ports
				if sp.TargetPort.IntVal > 0 {
					port = sp.TargetPort.IntVal
				} else {
					// Named port - use common defaults
					port = 8080
				}
				break
			}
		}
		if port == 0 && len(svc.Spec.Ports) > 0 {
			if svc.Spec.Ports[0].TargetPort.IntVal > 0 {
				port = svc.Spec.Ports[0].TargetPort.IntVal
			} else {
				port = 8080 // Default for mimir components
			}
		}

		candidate := &ProviderInfo{
			Type:      "mimir",
			Found:     true,
			Namespace: svc.Namespace,
			Service:   svc.Name,
			URL:       fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/prometheus", svc.Name, svc.Namespace, port),
			Port:      port,
		}
		// Insert by priority (higher priority first)
		inserted := false
		for i, c := range candidates {
			existingPriority := 0
			for j, pattern := range mimirServicePatterns {
				if strings.HasPrefix(strings.ToLower(c.Service), pattern) {
					existingPriority = len(mimirServicePatterns) - j
					break
				}
			}
			if priority > existingPriority {
				candidates = append(candidates[:i], append([]*ProviderInfo{candidate}, candidates[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			candidates = append(candidates, candidate)
		}
	}

	// Candidates are returned highest-priority first without a connectivity
	// check (we trust k8s service discovery); connectivity is verified on the
	// first actual query.
	return candidates, nil
}

func (p *MimirProvider) verifyConnectivity(cluster string, info *ProviderInfo) bool {
	podName := p.findMimirPod(cluster, info.Namespace, info.Service)
	if podName == "" {
		return false
	}

	targetPort := info.Port
	if targetPort == 0 {
		targetPort = 8080
	}

	pf, err := p.k8s.CreatePortForward(cluster, info.Namespace, podName, int(targetPort))
	if err != nil {
		return false
	}
	defer func() {
		_ = p.k8s.StopPortForward(pf.ID)
	}()

	time.Sleep(200 * time.Millisecond)

	client := &http.Client{Timeout: 3 * time.Second}
	endpoints := []string{
		fmt.Sprintf("http://localhost:%d/prometheus/api/v1/status/buildinfo", pf.LocalPort),
		fmt.Sprintf("http://localhost:%d/prometheus/api/v1/query?query=up", pf.LocalPort),
		fmt.Sprintf("http://localhost:%d/api/v1/status/buildinfo", pf.LocalPort),
		fmt.Sprintf("http://localhost:%d/ready", pf.LocalPort),
	}

	for _, endpoint := range endpoints {
		resp, err := client.Get(endpoint)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return true
		}
	}
	return false
}

func (p *MimirProvider) IsInstalled(cluster string) bool {
	info, err := p.Detect(cluster)
	if err != nil {
		return false
	}
	return info.Found
}

func (p *MimirProvider) Install(cluster string, namespace string) error {
	return fmt.Errorf("mimir installation is not supported - please install via Helm chart: helm install mimir grafana/mimir-distributed")
}

func (p *MimirProvider) QueryMetrics(cluster string, query MetricQuery) (*MetricResponse, error) {
	info, err := p.Detect(cluster)
	if err != nil || !info.Found {
		return nil, fmt.Errorf("mimir not detected in cluster")
	}

	pfInfo, err := p.getOrCreatePortForward(cluster, info)
	if err != nil {
		return nil, fmt.Errorf("failed to get port forward: %w", err)
	}

	promQL := p.buildPromQuery(query)
	timeRange := p.parseTimeRange(query.TimeRange)
	step := query.Step
	if step == "" {
		step = p.calculateStep(timeRange)
	}

	endTime := time.Now()
	startTime := endTime.Add(-timeRange)

	queryURL := fmt.Sprintf("http://localhost:%d/prometheus/api/v1/query_range", pfInfo.PortForward.LocalPort)
	params := url.Values{}
	params.Set("query", promQL)
	params.Set("start", fmt.Sprintf("%d", startTime.Unix()))
	params.Set("end", fmt.Sprintf("%d", endTime.Unix()))
	params.Set("step", step)

	fullURL := queryURL + "?" + params.Encode()
	resp, err := p.doMimirRequest(cluster, pfInfo, fullURL)
	if err != nil {
		p.portForwardPool.Delete("mimir-" + cluster)
		return nil, fmt.Errorf("failed to query mimir: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mimir returned http %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "json") {
		return nil, fmt.Errorf("mimir returned non-JSON response (content-type %q)", ct)
	}

	var promResponse struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]interface{}   `json:"values"`
			} `json:"result"`
		} `json:"data"`
		ErrorType string `json:"errorType,omitempty"`
		Error     string `json:"error,omitempty"`
	}

	if err := sonic.Unmarshal(body, &promResponse); err != nil {
		return nil, fmt.Errorf("mimir returned unexpected response")
	}

	if promResponse.Status != "success" {
		return nil, fmt.Errorf("mimir query failed: %s - %s", promResponse.ErrorType, promResponse.Error)
	}

	response := &MetricResponse{
		Labels: []string{},
		Values: []float64{},
		Unit:   p.getMetricUnit(query.MetricType),
	}

	if len(promResponse.Data.Result) == 0 {
		return response, nil
	}

	for _, value := range promResponse.Data.Result[0].Values {
		if len(value) >= 2 {
			timestamp, ok := value[0].(float64)
			if !ok {
				continue
			}
			var floatVal float64
			switch v := value[1].(type) {
			case string:
				if _, err := fmt.Sscanf(v, "%f", &floatVal); err != nil {
					continue
				}
			case float64:
				floatVal = v
			default:
				continue
			}
			response.Labels = append(response.Labels, time.Unix(int64(timestamp), 0).Format("15:04"))
			response.Values = append(response.Values, floatVal)
		}
	}

	return response, nil
}

func (p *MimirProvider) buildPromQuery(query MetricQuery) string {
	switch query.MetricType {
	case "cpu":
		if query.ContainerName != "" {
			return fmt.Sprintf(`rate(container_cpu_usage_seconds_total{namespace="%s",pod="%s",container="%s"}[5m]) * 1000`, query.Namespace, query.PodName, query.ContainerName)
		}
		return fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace="%s",pod="%s",container!=""}[5m])) * 1000`, query.Namespace, query.PodName)
	case "memory":
		if query.ContainerName != "" {
			return fmt.Sprintf(`container_memory_usage_bytes{namespace="%s",pod="%s",container="%s"}`, query.Namespace, query.PodName, query.ContainerName)
		}
		return fmt.Sprintf(`sum(container_memory_usage_bytes{namespace="%s",pod="%s",container!=""})`, query.Namespace, query.PodName)
	case "network_rx":
		return fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{namespace="%s",pod="%s"}[5m])) / 1024`, query.Namespace, query.PodName)
	case "network_tx":
		return fmt.Sprintf(`sum(rate(container_network_transmit_bytes_total{namespace="%s",pod="%s"}[5m])) / 1024`, query.Namespace, query.PodName)
	case "disk_read":
		return fmt.Sprintf(`sum(rate(container_fs_reads_bytes_total{namespace="%s",pod="%s"}[5m])) / 1024`, query.Namespace, query.PodName)
	case "disk_write":
		return fmt.Sprintf(`sum(rate(container_fs_writes_bytes_total{namespace="%s",pod="%s"}[5m])) / 1024`, query.Namespace, query.PodName)
	default:
		return fmt.Sprintf(`up{namespace="%s",pod="%s"}`, query.Namespace, query.PodName)
	}
}

func (p *MimirProvider) QueryWorkloadMetrics(cluster string, query WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	info, err := p.Detect(cluster)
	if err != nil || !info.Found {
		return nil, fmt.Errorf("mimir not detected in cluster")
	}

	pfInfo, err := p.getOrCreatePortForward(cluster, info)
	if err != nil {
		return nil, fmt.Errorf("failed to get port forward: %w", err)
	}

	podRegex := strings.Join(query.PodNames, "|")
	promQL := p.buildWorkloadPromQuery(query.Namespace, podRegex, query.MetricType)
	timeRange := p.parseTimeRange(query.TimeRange)
	step := query.Step
	if step == "" {
		step = p.calculateStep(timeRange)
	}

	endTime := time.Now()
	startTime := endTime.Add(-timeRange)

	queryURL := fmt.Sprintf("http://localhost:%d/prometheus/api/v1/query_range", pfInfo.PortForward.LocalPort)
	params := url.Values{}
	params.Set("query", promQL)
	params.Set("start", fmt.Sprintf("%d", startTime.Unix()))
	params.Set("end", fmt.Sprintf("%d", endTime.Unix()))
	params.Set("step", step)

	fullURL := queryURL + "?" + params.Encode()
	resp, err := p.doMimirRequest(cluster, pfInfo, fullURL)
	if err != nil {
		p.portForwardPool.Delete("mimir-" + cluster)
		return nil, fmt.Errorf("failed to query mimir: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mimir returned http %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "json") {
		return nil, fmt.Errorf("mimir returned non-JSON response (content-type %q)", ct)
	}

	var promResponse struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]interface{}   `json:"values"`
			} `json:"result"`
		} `json:"data"`
		ErrorType string `json:"errorType,omitempty"`
		Error     string `json:"error,omitempty"`
	}

	if err := sonic.Unmarshal(body, &promResponse); err != nil {
		return nil, fmt.Errorf("mimir returned unexpected response")
	}

	if promResponse.Status != "success" {
		return nil, fmt.Errorf("mimir query failed: %s - %s", promResponse.ErrorType, promResponse.Error)
	}

	response := &WorkloadMetricResponse{
		Pods: make(map[string]*MetricResponse),
	}
	unit := p.getMetricUnit(query.MetricType)

	for _, result := range promResponse.Data.Result {
		podName := result.Metric["pod"]
		if podName == "" {
			continue
		}
		podMetrics := &MetricResponse{
			Labels: []string{},
			Values: []float64{},
			Unit:   unit,
		}
		for _, value := range result.Values {
			if len(value) >= 2 {
				timestamp, ok := value[0].(float64)
				if !ok {
					continue
				}
				var floatVal float64
				switch v := value[1].(type) {
				case string:
					if _, err := fmt.Sscanf(v, "%f", &floatVal); err != nil {
						continue
					}
				case float64:
					floatVal = v
				default:
					continue
				}
				podMetrics.Labels = append(podMetrics.Labels, time.Unix(int64(timestamp), 0).Format("15:04"))
				podMetrics.Values = append(podMetrics.Values, floatVal)
			}
		}
		response.Pods[podName] = podMetrics
	}

	return response, nil
}

func (p *MimirProvider) buildWorkloadPromQuery(namespace, podRegex, metricType string) string {
	switch metricType {
	case "cpu":
		return fmt.Sprintf(`sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s",container!=""}[5m])) * 1000`, namespace, podRegex)
	case "memory":
		return fmt.Sprintf(`sum by (pod) (container_memory_usage_bytes{namespace="%s",pod=~"%s",container!=""})`, namespace, podRegex)
	case "network_rx":
		return fmt.Sprintf(`sum by (pod) (rate(container_network_receive_bytes_total{namespace="%s",pod=~"%s"}[5m])) / 1024`, namespace, podRegex)
	case "network_tx":
		return fmt.Sprintf(`sum by (pod) (rate(container_network_transmit_bytes_total{namespace="%s",pod=~"%s"}[5m])) / 1024`, namespace, podRegex)
	default:
		return fmt.Sprintf(`sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s",container!=""}[5m])) * 1000`, namespace, podRegex)
	}
}

func (p *MimirProvider) parseTimeRange(timeRange string) time.Duration {
	switch timeRange {
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	}
	if len(timeRange) > 1 {
		unit := timeRange[len(timeRange)-1]
		valueStr := timeRange[:len(timeRange)-1]
		var value int
		if _, err := fmt.Sscanf(valueStr, "%d", &value); err == nil && value > 0 {
			switch unit {
			case 'm':
				return time.Duration(value) * time.Minute
			case 'h':
				return time.Duration(value) * time.Hour
			case 'd':
				return time.Duration(value) * 24 * time.Hour
			}
		}
	}
	return 15 * time.Minute
}

func (p *MimirProvider) calculateStep(duration time.Duration) string {
	points := 50
	stepSeconds := int(duration.Seconds() / float64(points))
	if stepSeconds < 15 {
		return "15s"
	} else if stepSeconds < 30 {
		return "30s"
	} else if stepSeconds < 60 {
		return "1m"
	} else if stepSeconds < 300 {
		return "5m"
	}
	return "15m"
}

func (p *MimirProvider) getMetricUnit(metricType string) string {
	switch metricType {
	case "cpu":
		return "millicores"
	case "memory":
		return "bytes"
	case "network_rx", "network_tx":
		return "KB/s"
	case "disk_read", "disk_write":
		return "KB/s"
	default:
		return ""
	}
}
