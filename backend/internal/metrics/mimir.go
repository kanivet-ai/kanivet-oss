package metrics

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	"io"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
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
		if jsonv2.Unmarshal(body, &probe) == nil && probe.Status == "success" && len(probe.Data) > 0 {
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
	if jsonv2.Unmarshal(body, &stats) != nil {
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

// mimirPoolKey identifies a port-forward by the Service it reaches.
func mimirPoolKey(cluster string, info *ProviderInfo) string {
	return "mimir|" + cluster + "|" + info.Namespace + "/" + info.Service
}

// forgetDetection drops the cached provider so the next query re-detects.
func (p *MimirProvider) forgetDetection(cluster string) {
	p.cache.Delete(p.cache.BuildKey("mimir-info", cluster))
}

func (p *MimirProvider) getOrCreatePortForward(cluster string, info *ProviderInfo) (*PortForwardInfo, error) {
	cacheKey := mimirPoolKey(cluster, info)
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
		// Nothing ready behind the Service any more: forget the detection so
		// the next query re-detects instead of failing until the cache expires.
		p.forgetDetection(cluster)
		return nil, fmt.Errorf("no ready pod behind %s/%s", info.Namespace, info.Service)
	}
	pf, err := p.k8s.CreatePortForward(cluster, info.Namespace, podName, int(targetPort))
	if err != nil {
		p.forgetDetection(cluster)
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
		BasePath:    "/prometheus",
	}
	p.portForwardPool.Store(cacheKey, pfInfo)
	time.Sleep(100 * time.Millisecond)
	return pfInfo, nil
}

// findMimirPod returns a ready pod behind the Service, or "" when none is.
func (p *MimirProvider) findMimirPod(cluster, namespace, serviceName string) string {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	return listServiceBackends(ctx, clientset, svc).ReadyPod
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
	return detectCached(p.cache, p.cache.BuildKey("mimir-info", cluster), func() (*ProviderInfo, error) {
		return p.detectInternal(cluster)
	})
}

// DetectAllMimirServices returns every Mimir/Cortex gateway-like Service in
// the cluster, best first, so the UI can let the operator pick which one to
// query. This is discovery only — nothing here has been probed.
func (p *MimirProvider) DetectAllMimirServices(cluster string) ([]*ProviderInfo, error) {
	discovered, err := p.mimirCandidates(cluster)
	if err != nil {
		return nil, err
	}
	infos := make([]*ProviderInfo, 0, len(discovered))
	for _, d := range discovered {
		info := *d.info
		infos = append(infos, &info)
	}
	return infos, nil
}

// detectInternal verifies the discovered gateways best-first: ready pods behind
// the Service, then a probe of /prometheus/api/v1 through a port-forward with
// the configured tenant header. A 401 still counts as found — the gateway is
// there, it just needs a tenant — and is flagged so the UI can ask for one.
//
// A user-chosen service overrides auto-discovery: clusters can expose several
// Mimir gateways (a host-level one plus vcluster-mapped copies) and only one
// holds the container metrics we query. When the operator picked one it is the
// only one tried; silently falling back to another would query the wrong data.
func (p *MimirProvider) detectInternal(cluster string) (*ProviderInfo, error) {
	discovered, err := p.mimirCandidates(cluster)
	if err != nil {
		return nil, err
	}
	if len(discovered) == 0 {
		return &ProviderInfo{Type: "mimir", Found: false, Reason: "no Mimir or Cortex gateway service in this cluster"}, nil
	}

	toTry := discovered
	if p.serviceLookup != nil {
		if ns, svc := p.serviceLookup(cluster); svc != "" {
			toTry = nil
			for _, d := range discovered {
				if d.info.Service == svc && (ns == "" || d.info.Namespace == ns) {
					toTry = append(toTry, d)
				}
			}
			if len(toTry) == 0 {
				return &ProviderInfo{Type: "mimir", Found: false, Reason: fmt.Sprintf("the chosen Mimir service %s/%s no longer exists", ns, svc)}, nil
			}
		}
	}

	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	var headers map[string]string
	if p.tenantLookup != nil {
		if tenant := p.tenantLookup(cluster); tenant != "" {
			headers = map[string]string{"X-Scope-OrgID": tenant}
		}
	}

	var reasons []string
	for i, d := range toTry {
		if i >= maxVerifiedCandidates {
			break
		}
		where := d.info.Namespace + "/" + d.info.Service

		backends := listServiceBackends(ctx, clientset, &d.svc)
		if backends.ReadyPod == "" {
			reasons = append(reasons, where+": "+backends.describe())
			continue
		}

		port := resolvePodPort(ctx, clientset, d.info.Namespace, backends.ReadyPod, d.svcPort, 8080)
		outcome := probePrometheusAPI(p.k8s, cluster, d.info.Namespace, backends.ReadyPod, port, "/prometheus", headers)

		info := *d.info
		info.Port = port
		switch {
		case outcome.OK:
			info.Found = true
			info.Verified = true
			info.Version = outcome.Version
			log.Printf("[Mimir] Using %s %s (pod %s:%d, version %q) in cluster %s", info.Flavor, where, backends.ReadyPod, port, outcome.Version, cluster)
			return &info, nil
		case outcome.NeedsTenant:
			info.Found = true
			info.NeedsTenant = true
			info.Reason = "reachable, but it needs a tenant (X-Scope-OrgID) before it will answer"
			log.Printf("[Mimir] %s in cluster %s answers 401 — tenant required", where, cluster)
			return &info, nil
		default:
			reasons = append(reasons, where+": "+outcome.Detail)
		}
	}

	reason := joinReasons(reasons, "no Mimir gateway could be verified")
	log.Printf("[Mimir] No usable provider in cluster %s: %s", cluster, reason)
	return &ProviderInfo{Type: "mimir", Found: false, Reason: reason}, nil
}

// mimirDiscovered is a Service that looks like a Mimir/Cortex query gateway,
// before verification.
type mimirDiscovered struct {
	info    *ProviderInfo
	svc     v1.Service
	svcPort v1.ServicePort
	score   int
}

func (p *MimirProvider) mimirCandidates(cluster string) ([]mimirDiscovered, error) {
	cacheKey := p.cache.BuildKey("mimir-candidates", cluster)
	cached, err := p.cache.GetOrSet(cacheKey, 60*time.Second, func() (any, error) {
		return p.mimirCandidatesUncached(cluster)
	})
	if err != nil {
		return nil, err
	}
	return cached.([]mimirDiscovered), nil
}

func (p *MimirProvider) mimirCandidatesUncached(cluster string) ([]mimirDiscovered, error) {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// One call for every Service, served from the apiserver watch cache
	// instead of a quorum etcd read.
	services, err := clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	return mimirCandidatesFromServices(services.Items), nil
}

// mimirCandidatesFromServices classifies and ranks Services, best first.
func mimirCandidatesFromServices(services []v1.Service) []mimirDiscovered {
	var out []mimirDiscovered
	for _, svc := range services {
		if d, ok := classifyMimirService(svc); ok {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].svc.Namespace != out[j].svc.Namespace {
			return out[i].svc.Namespace < out[j].svc.Namespace
		}
		return out[i].svc.Name < out[j].svc.Name
	})
	return out
}

// mimirExcludedFragments mark Mimir/Cortex components (and neighbours in the
// same chart) that do not serve the query API.
var mimirExcludedFragments = []string{
	"ingester", "distributor", "compactor", "store-gateway", "storegateway",
	"alertmanager", "ruler", "exporter", "memcached", "cache", "rollout-operator",
	"gossip", "admin-api", "tokengen", "continuous-test", "smoke-test", "graphite",
	"operator", "meta-monitoring", "minio", "grafana", "loki", "tempo", "pyroscope",
	"alloy", "agent", "scheduler",
}

// classifyMimirService decides whether a Service is a Mimir or Cortex
// endpoint we can query (nginx/gateway, query-frontend, querier, the "read"
// half of the simple-scalable layout, or a monolithic single service), picks
// the port to talk to, and ranks it: gateways first, then well-known
// namespaces; vcluster-synced copies ("-x-" in the name) rank behind the host
// service they mirror.
func classifyMimirService(svc v1.Service) (mimirDiscovered, bool) {
	name := strings.ToLower(svc.Name)
	ns := strings.ToLower(svc.Namespace)
	labels := lowerLabels(svc.Labels)
	appName := labels["app.kubernetes.io/name"]
	component := labels["app.kubernetes.io/component"]
	haystack := strings.Join([]string{name, appName, component, labels["app"], labels["component"], labels["name"]}, " ")

	flavor := ""
	switch {
	case containsAny(haystack, "mimir", "enterprise-metrics", "gem-gateway"):
		flavor = "mimir"
	case strings.Contains(haystack, "cortex"):
		flavor = "cortex"
	default:
		return mimirDiscovered{}, false
	}
	for _, frag := range mimirExcludedFragments {
		if strings.Contains(haystack, frag) {
			return mimirDiscovered{}, false
		}
	}
	if strings.HasSuffix(name, "-write") || strings.HasSuffix(name, "-backend") {
		return mimirDiscovered{}, false
	}

	role := 0
	switch {
	case containsAny(haystack, "nginx", "gateway"):
		role = 3
	case containsAny(haystack, "query-frontend", "queryfrontend", "query_frontend"), strings.HasSuffix(name, "-read"):
		role = 2
	case strings.Contains(haystack, "querier"):
		role = 1
	default:
		// Only a monolithic single service qualifies here. Anything else that
		// carries the name is a component we do not know how to query.
		monolith := name == flavor || component == "" || component == flavor ||
			containsAny(component, "monolith", "single-binary", "all")
		if !monolith || (name != flavor && appName != flavor) {
			return mimirDiscovered{}, false
		}
	}

	score := role*100 + rankMonitoringNamespace(ns)
	if strings.Contains(name, "-x-") {
		score -= 30
	}
	if svc.Spec.ClusterIP == v1.ClusterIPNone {
		score -= 5
	}
	if isBundledNamespace(ns) {
		score -= 100
	}

	svcPort, _ := pickServicePort(svc.Spec.Ports, []int32{80, 8080}, []string{"http-metric", "http-metrics", "http"})
	port := svcPort.TargetPort.IntVal
	if port == 0 {
		port = 8080 // component default; resolved against the pod at verification time
	}
	info := &ProviderInfo{
		Type:      "mimir",
		Found:     true,
		Flavor:    flavor,
		Namespace: svc.Namespace,
		Service:   svc.Name,
		Port:      port,
		Path:      "/prometheus",
		URL:       fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/prometheus", svc.Name, svc.Namespace, svcPort.Port),
	}
	return mimirDiscovered{info: info, svc: svc, svcPort: svcPort, score: score}, true
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
		p.portForwardPool.Delete(mimirPoolKey(cluster, info))
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

	if err := jsonv2.Unmarshal(body, &promResponse); err != nil {
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
		p.portForwardPool.Delete(mimirPoolKey(cluster, info))
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

	if err := jsonv2.Unmarshal(body, &promResponse); err != nil {
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
