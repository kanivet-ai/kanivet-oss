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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type PrometheusProvider struct {
	k8s             k8s.Interface
	cache           *cache.Cache
	portForwardPool sync.Map // cluster -> *PortForwardInfo
	recreateState   sync.Map // cluster -> *recreateTracker
}

type PortForwardInfo struct {
	PortForward *k8s.PortForward
	HTTPClient  *http.Client
	LastUsed    time.Time
	Mutex       sync.Mutex
}

// recreateTracker debounces port-forward recreation per cluster. Without it a
// single misconfigured prometheus (e.g. service exposing port 80 but pod
// listening on 9090) caused the metrics stream to kill+rebuild the port-forward
// on every failed query — ~7 recreates/second under sustained subscriptions.
type recreateTracker struct {
	mu               sync.Mutex
	lastRecreate     time.Time
	consecutiveFails int
	lastSuccess      time.Time
}

// minRecreateInterval is the minimum time between successive port-forward
// recreates for the same cluster. 30s lines up with the typical metrics stream
// interval — at most one recreate per stream tick per cluster.
const minRecreateInterval = 30 * time.Second

// failuresBeforeProviderReset is how many consecutive failed recreates we
// tolerate before invalidating the cached prometheus-info. Hitting this means
// the port we detected is almost certainly wrong; busting the cache forces
// re-detection on the next call.
const failuresBeforeProviderReset = 3

func NewPrometheusProvider(k8sClient k8s.Interface, cacheInstance *cache.Cache) *PrometheusProvider {
	p := &PrometheusProvider{
		k8s:   k8sClient,
		cache: cacheInstance,
	}

	// Start cleanup goroutine for unused port forwards
	go p.cleanupUnusedPortForwards()

	// Start keepalive goroutine to prevent port forwards from timing out
	go p.keepAlivePortForwards()

	return p
}

// isPortForwardLikelyDead returns true for transport-level errors that suggest
// the port-forward stream is gone or never reached the target — connection
// refused, EOF on the open connection, "lost connection to pod" reported by
// the SPDY stream owner, or a TCP reset. Application errors (4xx/5xx response
// bodies) do NOT count and are not retried by recreating the tunnel.
func isPortForwardLikelyDead(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection refused",
		"eof",
		"lost connection",
		"connection reset",
		"broken pipe",
		"unexpected eof",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// trackerFor returns the per-cluster recreate tracker, creating it on first use.
func (p *PrometheusProvider) trackerFor(cluster string) *recreateTracker {
	if t, ok := p.recreateState.Load(cluster); ok {
		return t.(*recreateTracker)
	}
	t, _ := p.recreateState.LoadOrStore(cluster, &recreateTracker{})
	return t.(*recreateTracker)
}

// shouldRecreate decides whether the caller is allowed to tear down and rebuild
// the port-forward for a cluster right now. Returns false if a recreate
// happened too recently — the caller should fail the current query and let the
// outer retry / next stream tick try again. Returns true and stamps the tracker
// when a recreate is permitted.
func (p *PrometheusProvider) shouldRecreate(cluster string) bool {
	t := p.trackerFor(cluster)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.lastRecreate.IsZero() && time.Since(t.lastRecreate) < minRecreateInterval {
		return false
	}
	t.lastRecreate = time.Now()
	t.consecutiveFails++
	return true
}

// recordQuerySuccess resets the failure counter — a successful Prometheus
// query proves the current port-forward target is correct.
func (p *PrometheusProvider) recordQuerySuccess(cluster string) {
	t := p.trackerFor(cluster)
	t.mu.Lock()
	t.consecutiveFails = 0
	t.lastSuccess = time.Now()
	t.mu.Unlock()
}

// maybeInvalidateProvider drops the cached prometheus-info entry when we've
// recreated the port-forward many times without a single successful query —
// the most likely explanation is that the cached detection picked the wrong
// service (wrong namespace or wrong port). Re-detection runs on the next call.
func (p *PrometheusProvider) maybeInvalidateProvider(cluster string) {
	t := p.trackerFor(cluster)
	t.mu.Lock()
	fails := t.consecutiveFails
	if fails >= failuresBeforeProviderReset {
		t.consecutiveFails = 0
	}
	t.mu.Unlock()
	if fails >= failuresBeforeProviderReset {
		p.cache.DeleteByPrefix(p.cache.BuildKey("prometheus-info", cluster))
		log.Printf("[Prometheus] Invalidated cached provider info for cluster %s after %d consecutive failed recreates", cluster, fails)
	}
}

// Get or create a port forward for the cluster
func (p *PrometheusProvider) getOrCreatePortForward(cluster string, promInfo *ProviderInfo) (*PortForwardInfo, error) {
	if pfi, ok := p.portForwardPool.Load(cluster); ok {
		pfInfo := pfi.(*PortForwardInfo)
		pfInfo.Mutex.Lock()
		pfInfo.LastUsed = time.Now()
		pfInfo.Mutex.Unlock()
		return pfInfo, nil
	}

	// Create new port forward - use detected port or default to 9090
	targetPort := promInfo.Port
	if targetPort == 0 {
		targetPort = 9090
	}
	pf, err := p.k8s.CreatePortForward(cluster, promInfo.Namespace, p.findPrometheusPod(cluster, promInfo.Namespace), int(targetPort))
	if err != nil {
		return nil, fmt.Errorf("failed to create port forward: %w", err)
	}

	// Create optimized HTTP client
	httpClient := &http.Client{
		Timeout: 5 * time.Second, // Reduced for faster failure detection
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
			DisableKeepAlives:   false,
			// Fast failure on connection issues
			DialContext: (&net.Dialer{
				Timeout:   1 * time.Second, // Fast connection timeout
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	pfInfo := &PortForwardInfo{
		PortForward: pf,
		HTTPClient:  httpClient,
		LastUsed:    time.Now(),
	}

	p.portForwardPool.Store(cluster, pfInfo)

	// Give port forward a moment to be ready
	time.Sleep(100 * time.Millisecond) // Reduced from 500ms

	return pfInfo, nil
}

// Cleanup unused port forwards every 5 minutes
func (p *PrometheusProvider) cleanupUnusedPortForwards() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		p.portForwardPool.Range(func(key, value interface{}) bool {
			pfInfo := value.(*PortForwardInfo)
			pfInfo.Mutex.Lock()

			// Close port forwards unused for more than 10 minutes
			if time.Since(pfInfo.LastUsed) > 10*time.Minute {
				log.Printf("Cleaning up unused port forward for cluster %s", key)
				if err := p.k8s.StopPortForward(pfInfo.PortForward.ID); err != nil {
					log.Printf("Failed to stop port forward %s: %v", pfInfo.PortForward.ID, err)
				}
				p.portForwardPool.Delete(key)
			}

			pfInfo.Mutex.Unlock()
			return true
		})
	}
}

// Keep port forwards alive by sending periodic health checks
func (p *PrometheusProvider) keepAlivePortForwards() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		p.portForwardPool.Range(func(key, value interface{}) bool {
			pfInfo := value.(*PortForwardInfo)
			pfInfo.Mutex.Lock()

			// Only send keepalive if the port forward was used recently (within last 5 minutes)
			if time.Since(pfInfo.LastUsed) < 5*time.Minute {
				// Send a lightweight health check to keep the connection alive
				healthURL := fmt.Sprintf("http://localhost:%d/-/ready", pfInfo.PortForward.LocalPort)
				healthClient := &http.Client{Timeout: 2 * time.Second}
				resp, err := healthClient.Get(healthURL)
				if err == nil {
					if err := resp.Body.Close(); err != nil {
						log.Printf("Failed to close response body: %v", err)
					}
					if resp.StatusCode == 200 {
						log.Printf("Keepalive successful for port forward on cluster %s", key)
					} else {
						log.Printf("Keepalive failed for port forward on cluster %s: status %d", key, resp.StatusCode)
					}
				} else {
					log.Printf("Keepalive failed for port forward on cluster %s: %v", key, err)
				}
			}

			pfInfo.Mutex.Unlock()
			return true
		})
	}
}

func (p *PrometheusProvider) GetName() string {
	return "prometheus"
}

func (p *PrometheusProvider) Detect(cluster string) (*ProviderInfo, error) {
	cacheKey := p.cache.BuildKey("prometheus-info", cluster)

	data, err := p.cache.GetOrSet(cacheKey, 30*time.Minute, func() (interface{}, error) {
		return p.detectInternal(cluster)
	})

	if err != nil {
		return nil, err
	}

	return data.(*ProviderInfo), nil
}

func (p *PrometheusProvider) detectInternal(cluster string) (*ProviderInfo, error) {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Single API call to get ALL services across all namespaces
	services, err := clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	type scored struct {
		info  *ProviderInfo
		score int
	}
	var candidates []scored
	for _, svc := range services.Items {
		score, ok := scorePrometheusService(svc)
		if !ok {
			continue
		}
		port := pickPrometheusTargetPort(svc.Spec.Ports)
		candidates = append(candidates, scored{
			info: &ProviderInfo{
				Type:      "prometheus",
				Found:     true,
				Namespace: svc.Namespace,
				Service:   svc.Name,
				URL:       fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", svc.Name, svc.Namespace, port),
				Port:      port,
			},
			score: score,
		})
	}

	if len(candidates) == 0 {
		return &ProviderInfo{Type: "prometheus", Found: false}, nil
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}
	if len(candidates) > 1 {
		log.Printf("[Prometheus] Detected %d candidates in cluster %s, picked %s/%s (score=%d)",
			len(candidates), cluster, best.info.Namespace, best.info.Service, best.score)
	}
	return best.info, nil
}

// scorePrometheusService classifies a Service as a viable cluster-wide
// prometheus and returns a relative score (higher is better). Returns ok=false
// if the service should be skipped entirely.
//
// We deliberately exclude bundled prometheus instances that are scoped to a
// single tool (opencost, prometheus-adapter, thanos sidecars, karma) — those
// have limited retention and limited metric coverage and are not safe defaults
// for general dashboard queries. We also rank by namespace because the
// well-known monitoring namespaces nearly always host the canonical instance.
func scorePrometheusService(svc v1.Service) (int, bool) {
	name := strings.ToLower(svc.Name)
	ns := strings.ToLower(svc.Namespace)

	if !strings.Contains(name, "prometheus") {
		return 0, false
	}

	// Hard exclusions: services that contain "prometheus" in their name but are
	// not a queryable cluster-wide TSDB.
	excludedNameFragments := []string{
		"alertmanager", "pushgateway", "operator", "node-exporter",
		"adapter",  // prometheus-adapter is an HPA metrics shim, not a TSDB
		"thanos",   // thanos-prometheus is for cross-cluster federation queries
		"karma",    // karma is an alertmanager UI
		"blackbox", // prometheus-blackbox-exporter
		"snmp",     // prometheus-snmp-exporter
		"statsd",   // prometheus-statsd-exporter
		"msteams",  // prometheus-msteams notifier
	}
	for _, frag := range excludedNameFragments {
		if strings.Contains(name, frag) {
			return 0, false
		}
	}

	// Bundled prometheus instances live under another tool's namespace and
	// usually have curtailed retention. Skip them — the user's real prometheus
	// (if any) lives in a monitoring namespace.
	bundledNamespaces := []string{"opencost", "kubecost", "loki", "tempo", "grafana-cloud"}
	for _, bn := range bundledNamespaces {
		if ns == bn || strings.HasPrefix(ns, bn+"-") {
			return 0, false
		}
	}

	score := 0

	// Namespace ranking. Conventional monitoring namespaces dominate. Without
	// this, name-based ranking alone picks any service called
	// "*-prometheus-server" first — including bundled ones when the canonical
	// monitoring namespace doesn't exist.
	switch {
	case ns == "monitoring":
		score += 100
	case ns == "prometheus":
		score += 95
	case ns == "kube-prometheus-stack":
		score += 95
	case ns == "observability":
		score += 90
	case strings.Contains(ns, "monitor"):
		score += 60
	case strings.Contains(ns, "prom"):
		score += 50
	case strings.Contains(ns, "observ"):
		score += 50
	default:
		// Penalize unknown namespaces slightly so a same-name candidate in a
		// well-known namespace always wins.
		score -= 10
	}

	// Name ranking. Prefer the canonical service names produced by the
	// upstream helm charts and operators.
	switch {
	case name == "prometheus-operated":
		score += 50 // prometheus-operator's headless service for Prometheus CRD
	case strings.Contains(name, "kube-prometheus-stack-prometheus"):
		score += 45
	case strings.Contains(name, "kube-prometheus-prometheus"):
		score += 40
	case strings.Contains(name, "prometheus-server"):
		score += 30
	case name == "prometheus":
		score += 25
	default:
		score += 10
	}

	return score, true
}

// pickPrometheusTargetPort returns the pod-side port to use when port-forwarding
// to a prometheus service. It prefers the canonical 9090 over any other exposed
// port, falls back to the named "http"/"web" port, then to the first non-zero
// TargetPort, and finally to 9090 as a last-ditch default. We always return the
// container TargetPort (what the pod listens on); the service Port is irrelevant
// for port-forwarding because that talks to the pod directly.
func pickPrometheusTargetPort(svcPorts []v1.ServicePort) int32 {
	resolveTarget := func(sp v1.ServicePort) int32 {
		if sp.TargetPort.IntVal > 0 {
			return sp.TargetPort.IntVal
		}
		return sp.Port
	}

	// 1. Service entry with Port==9090 wins outright (the canonical prom port).
	for _, sp := range svcPorts {
		if sp.Port == 9090 || sp.TargetPort.IntVal == 9090 {
			return resolveTarget(sp)
		}
	}
	// 2. Named "http"/"web" entry — typical for prom-helm-chart services.
	for _, sp := range svcPorts {
		if sp.Name == "http" || sp.Name == "web" {
			return resolveTarget(sp)
		}
	}
	// 3. Fall back to the first entry with a usable TargetPort.
	for _, sp := range svcPorts {
		if sp.TargetPort.IntVal > 0 {
			return sp.TargetPort.IntVal
		}
	}
	// 4. Last resort: prometheus default.
	return 9090
}

func (p *PrometheusProvider) verifyConnectivity(cluster string, info *ProviderInfo) bool {
	podName := p.findPrometheusPod(cluster, info.Namespace)
	if podName == "" {
		return false
	}

	targetPort := info.Port
	if targetPort == 0 {
		targetPort = 9090
	}

	pf, err := p.k8s.CreatePortForward(cluster, info.Namespace, podName, int(targetPort))
	if err != nil {
		log.Printf("Failed to create port forward for connectivity check: %v", err)
		return false
	}
	defer func() {
		_ = p.k8s.StopPortForward(pf.ID)
	}()

	time.Sleep(200 * time.Millisecond)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/-/ready", pf.LocalPort))
	if err != nil {
		resp, err = client.Get(fmt.Sprintf("http://localhost:%d/api/v1/status/config", pf.LocalPort))
		if err != nil {
			return false
		}
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200
}

func (p *PrometheusProvider) IsInstalled(cluster string) bool {
	info, err := p.Detect(cluster)
	if err != nil {
		return false
	}
	return info.Found
}

func (p *PrometheusProvider) Install(cluster string, namespace string) error {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		ns := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
	}

	dynamicClient, err := p.k8s.GetDynamicClient(cluster)
	if err != nil {
		return fmt.Errorf("failed to get dynamic client: %w", err)
	}

	if err := p.installWithManifests(ctx, dynamicClient, namespace); err != nil {
		return fmt.Errorf("failed to install Prometheus: %w", err)
	}

	// Wait for deployment to be ready (up to 2 minutes)
	log.Printf("Waiting for Prometheus deployment to be ready...")
	for i := 0; i < 24; i++ {
		time.Sleep(5 * time.Second)
		deploy, err := clientset.AppsV1().Deployments(namespace).Get(ctx, "prometheus", metav1.GetOptions{})
		if err == nil && deploy.Status.ReadyReplicas > 0 {
			log.Printf("Prometheus deployment ready")
			break
		}
	}

	p.cache.DeleteByPrefix(p.cache.BuildKey("prometheus-info", cluster))

	return nil
}

func (p *PrometheusProvider) QueryMetrics(cluster string, query MetricQuery) (*MetricResponse, error) {
	promInfo, err := p.Detect(cluster)
	if err != nil || !promInfo.Found {
		return nil, fmt.Errorf("prometheus not found in cluster")
	}

	promQL := p.buildPromQL(query)

	timeRange := p.parseTimeRange(query.TimeRange)
	step := query.Step
	if step == "" {
		step = p.calculateStep(timeRange)
	}

	// Try to use existing port forward first
	var pfInfo *PortForwardInfo
	var resp *http.Response
	var queryErr error

	// Try up to 2 times (once with existing, once with new port forward)
	for attempt := 0; attempt < 2; attempt++ {
		pfInfo, err = p.getOrCreatePortForward(cluster, promInfo)
		if err != nil {
			return nil, err
		}

		endTime := time.Now()
		startTime := endTime.Add(-timeRange)

		log.Printf("[Prometheus] Query params - timeRange: %v, start: %s, end: %s, step: %s",
			timeRange, startTime.Format("15:04:05"), endTime.Format("15:04:05"), step)

		queryURL := fmt.Sprintf("http://localhost:%d/api/v1/query_range", pfInfo.PortForward.LocalPort)
		params := url.Values{}
		params.Set("query", promQL)
		params.Set("start", fmt.Sprintf("%d", startTime.Unix()))
		params.Set("end", fmt.Sprintf("%d", endTime.Unix()))
		params.Set("step", step)

		fullURL := queryURL + "?" + params.Encode()
		resp, queryErr = pfInfo.HTTPClient.Get(fullURL)
		if queryErr == nil {
			p.recordQuerySuccess(cluster)
			break
		}

		// On the first attempt only, treat transport-level failures as a sign
		// the port-forward may be dead. The recreate is throttled per-cluster
		// so a misconfigured target can't make us churn port-forwards on every
		// failed query (the previous behaviour caused ~7 recreates/sec when a
		// stream was subscribed to a wrong-port prometheus).
		if attempt == 0 && isPortForwardLikelyDead(queryErr) {
			if p.shouldRecreate(cluster) {
				log.Printf("[Prometheus] Recreating port forward for cluster %s (err: %v)", cluster, queryErr)
				p.portForwardPool.Delete(cluster)
				_ = p.k8s.StopPortForward(pfInfo.PortForward.ID)
				p.maybeInvalidateProvider(cluster)
				continue
			}
			log.Printf("[Prometheus] Suppressing port-forward recreate for cluster %s (recent recreate within %s)", cluster, minRecreateInterval)
		}

		return nil, fmt.Errorf("failed to query prometheus: %w", queryErr)
	}

	if resp == nil {
		return nil, fmt.Errorf("failed to query prometheus after retries: %w", queryErr)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
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
		return nil, fmt.Errorf("failed to parse prometheus response: %w", err)
	}

	if promResponse.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s - %s", promResponse.ErrorType, promResponse.Error)
	}

	response := &MetricResponse{
		Labels: []string{},
		Values: []float64{},
		Unit:   p.getMetricUnit(query.MetricType),
	}

	// Handle empty results gracefully
	if len(promResponse.Data.Result) == 0 {
		log.Printf("No data found for metric query: %s", promQL)
		// Return empty but valid response
		return response, nil
	}

	// Process the first result series
	for _, value := range promResponse.Data.Result[0].Values {
		if len(value) >= 2 {
			// Safely extract timestamp
			timestamp, ok := value[0].(float64)
			if !ok {
				log.Printf("Invalid timestamp format: %v", value[0])
				continue
			}

			// Safely extract value
			var floatVal float64
			switch v := value[1].(type) {
			case string:
				if _, err := fmt.Sscanf(v, "%f", &floatVal); err != nil {
					log.Printf("Failed to parse float value %s: %v", v, err)
					continue
				}
			case float64:
				floatVal = v
			case int:
				floatVal = float64(v)
			default:
				log.Printf("Invalid value format: %v", value[1])
				continue
			}

			// Skip NaN or Inf values
			if math.IsNaN(floatVal) || math.IsInf(floatVal, 0) {
				continue
			}

			response.Labels = append(response.Labels, time.Unix(int64(timestamp), 0).Format("15:04:05"))
			response.Values = append(response.Values, floatVal)
		}
	}

	// If we still have no data points after processing, log it
	if len(response.Values) == 0 {
		log.Printf("No valid data points found for metric query: %s", promQL)
	}

	return response, nil
}

// QueryWorkloadMetrics queries metrics for multiple pods in a single Prometheus query using regex
func (p *PrometheusProvider) QueryWorkloadMetrics(cluster string, query WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	if len(query.PodNames) == 0 {
		return &WorkloadMetricResponse{Pods: map[string]*MetricResponse{}}, nil
	}

	promInfo, err := p.Detect(cluster)
	if err != nil || !promInfo.Found {
		return nil, fmt.Errorf("prometheus not found in cluster")
	}

	// Build regex pattern for all pods
	podRegex := strings.Join(query.PodNames, "|")
	promQL := p.buildWorkloadPromQL(query.Namespace, podRegex, query.MetricType)

	timeRange := p.parseTimeRange(query.TimeRange)
	step := query.Step
	if step == "" {
		step = p.calculateStep(timeRange)
	}

	pfInfo, err := p.getOrCreatePortForward(cluster, promInfo)
	if err != nil {
		return nil, err
	}

	endTime := time.Now()
	startTime := endTime.Add(-timeRange)

	queryURL := fmt.Sprintf("http://localhost:%d/api/v1/query_range", pfInfo.PortForward.LocalPort)
	params := url.Values{}
	params.Set("query", promQL)
	params.Set("start", startTime.Format(time.RFC3339))
	params.Set("end", endTime.Format(time.RFC3339))
	params.Set("step", step)

	fullURL := queryURL + "?" + params.Encode()

	resp, err := pfInfo.HTTPClient.Get(fullURL)
	if err != nil {
		return nil, fmt.Errorf("failed to query prometheus: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
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
		return nil, fmt.Errorf("failed to parse prometheus response: %w", err)
	}

	if promResponse.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s - %s", promResponse.ErrorType, promResponse.Error)
	}

	// Group results by pod name
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
				case int:
					floatVal = float64(v)
				default:
					continue
				}

				if math.IsNaN(floatVal) || math.IsInf(floatVal, 0) {
					continue
				}

				podMetrics.Labels = append(podMetrics.Labels, time.Unix(int64(timestamp), 0).Format("15:04:05"))
				podMetrics.Values = append(podMetrics.Values, floatVal)
			}
		}

		response.Pods[podName] = podMetrics
	}

	log.Printf("[Prometheus] Workload query returned metrics for %d pods", len(response.Pods))
	return response, nil
}

// buildWorkloadPromQL builds a PromQL query for multiple pods using regex
func (p *PrometheusProvider) buildWorkloadPromQL(namespace, podRegex, metricType string) string {
	switch metricType {
	case "cpu":
		return fmt.Sprintf(`sum by (pod) (rate(container_cpu_usage_seconds_total{pod=~"%s",namespace="%s",container!="POD",container!=""}[5m])) * 1000`,
			podRegex, namespace)
	case "memory":
		return fmt.Sprintf(`sum by (pod) (container_memory_usage_bytes{pod=~"%s",namespace="%s",container!="POD",container!=""})`,
			podRegex, namespace)
	case "network_rx":
		return fmt.Sprintf(`sum by (pod) (rate(container_network_receive_bytes_total{pod=~"%s",namespace="%s"}[5m])) / 1024`,
			podRegex, namespace)
	case "network_tx":
		return fmt.Sprintf(`sum by (pod) (rate(container_network_transmit_bytes_total{pod=~"%s",namespace="%s"}[5m])) / 1024`,
			podRegex, namespace)
	case "disk_read":
		return fmt.Sprintf(`sum by (pod) (rate(container_fs_reads_bytes_total{pod=~"%s",namespace="%s"}[5m])) / 1024`,
			podRegex, namespace)
	case "disk_write":
		return fmt.Sprintf(`sum by (pod) (rate(container_fs_writes_bytes_total{pod=~"%s",namespace="%s"}[5m])) / 1024`,
			podRegex, namespace)
	default:
		return fmt.Sprintf(`up{pod=~"%s",namespace="%s"}`, podRegex, namespace)
	}
}

func (p *PrometheusProvider) findPrometheusPod(cluster string, namespace string) string {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	// Look for any pod with "prometheus" in its name
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, pod := range pods.Items {
			if pod.Status.Phase != v1.PodRunning {
				continue
			}

			podNameLower := strings.ToLower(pod.Name)
			if strings.Contains(podNameLower, "prometheus") {
				// Verify it has a prometheus container
				for _, container := range pod.Spec.Containers {
					imageLower := strings.ToLower(container.Image)
					if strings.Contains(imageLower, "prometheus") {
						log.Printf("Using Prometheus pod: %s", pod.Name)
						return pod.Name
					}
				}
			}
		}
	}

	return ""
}

func (p *PrometheusProvider) buildPromQL(query MetricQuery) string {
	baseQuery := ""

	// Node-level metrics
	// Note: We use "instance" label as that's where the node name is stored in most Prometheus setups
	// Some setups also have "kubernetes_io_hostname" but "instance" is more common
	if query.NodeName != "" {
		switch query.MetricType {
		case "cpu":
			// Node CPU usage in millicores - using container metrics aggregated by instance (node)
			// This works with cAdvisor metrics which are always available
			baseQuery = fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{instance="%s",container!="POD",container!=""}[5m])) * 1000`,
				query.NodeName)
		case "memory":
			// Node memory usage in bytes - sum of all container memory on the node
			baseQuery = fmt.Sprintf(`sum(container_memory_usage_bytes{instance="%s",container!="POD",container!=""})`,
				query.NodeName)
		case "network_rx":
			// Node network receive - aggregate all pod network on the node
			baseQuery = fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{instance="%s"}[5m])) / 1024`,
				query.NodeName)
		case "network_tx":
			// Node network transmit
			baseQuery = fmt.Sprintf(`sum(rate(container_network_transmit_bytes_total{instance="%s"}[5m])) / 1024`,
				query.NodeName)
		case "disk_read":
			// Node disk read
			baseQuery = fmt.Sprintf(`sum(rate(container_fs_reads_bytes_total{instance="%s"}[5m])) / 1024`,
				query.NodeName)
		case "disk_write":
			// Node disk write
			baseQuery = fmt.Sprintf(`sum(rate(container_fs_writes_bytes_total{instance="%s"}[5m])) / 1024`,
				query.NodeName)
		default:
			baseQuery = fmt.Sprintf(`up{instance="%s"}`, query.NodeName)
		}
		return baseQuery
	}

	// Pod-level metrics
	switch query.MetricType {
	case "cpu":
		// CPU usage in cores (rate gives cores, multiply by 1000 for millicores)
		if query.ContainerName != "" {
			baseQuery = fmt.Sprintf(`rate(container_cpu_usage_seconds_total{pod="%s",namespace="%s",container="%s"}[5m]) * 1000`,
				query.PodName, query.Namespace, query.ContainerName)
		} else {
			baseQuery = fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{pod="%s",namespace="%s",container!="POD"}[5m])) by (pod) * 1000`,
				query.PodName, query.Namespace)
		}
	case "memory":
		// Memory usage in bytes (frontend will format it)
		if query.ContainerName != "" {
			baseQuery = fmt.Sprintf(`container_memory_usage_bytes{pod="%s",namespace="%s",container="%s"}`,
				query.PodName, query.Namespace, query.ContainerName)
		} else {
			baseQuery = fmt.Sprintf(`sum(container_memory_usage_bytes{pod="%s",namespace="%s",container!="POD"}) by (pod)`,
				query.PodName, query.Namespace)
		}
	case "network_rx":
		baseQuery = fmt.Sprintf(`rate(container_network_receive_bytes_total{pod="%s",namespace="%s"}[5m]) / 1024`,
			query.PodName, query.Namespace)
	case "network_tx":
		baseQuery = fmt.Sprintf(`rate(container_network_transmit_bytes_total{pod="%s",namespace="%s"}[5m]) / 1024`,
			query.PodName, query.Namespace)
	case "disk_read":
		baseQuery = fmt.Sprintf(`rate(container_fs_reads_bytes_total{pod="%s",namespace="%s"}[5m]) / 1024`,
			query.PodName, query.Namespace)
	case "disk_write":
		baseQuery = fmt.Sprintf(`rate(container_fs_writes_bytes_total{pod="%s",namespace="%s"}[5m]) / 1024`,
			query.PodName, query.Namespace)
	default:
		baseQuery = fmt.Sprintf(`up{pod="%s",namespace="%s"}`, query.PodName, query.Namespace)
	}

	return baseQuery
}

func (p *PrometheusProvider) getMetricUnit(metricType string) string {
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

func (p *PrometheusProvider) parseTimeRange(timeRange string) time.Duration {
	// Handle standard ranges
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

	// Handle custom duration format (e.g., "180m" for 180 minutes, "3h" for 3 hours)
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

	// Default fallback
	return 15 * time.Minute
}

func (p *PrometheusProvider) calculateStep(duration time.Duration) string {
	// Reduce points for faster queries - 50 instead of 100
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
	} else {
		return "15m"
	}
}

func (p *PrometheusProvider) installWithManifests(ctx context.Context, dynamicClient dynamic.Interface, namespace string) error {
	manifests := p.generateManifests(namespace)

	for _, manifest := range manifests {
		gvr := p.getGVRForManifest(manifest)
		obj := &unstructured.Unstructured{Object: manifest}

		if gvr.Group == "rbac.authorization.k8s.io" || manifest["kind"] == "ClusterRole" || manifest["kind"] == "ClusterRoleBinding" {
			_, err := dynamicClient.Resource(gvr).Create(ctx, obj, metav1.CreateOptions{})
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				log.Printf("Failed to create %s: %v", manifest["kind"], err)
			}
		} else {
			_, err := dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				log.Printf("Failed to create %s: %v", manifest["kind"], err)
			}
		}
	}

	return nil
}

func (p *PrometheusProvider) getGVRForManifest(manifest map[string]interface{}) schema.GroupVersionResource {
	kind := manifest["kind"].(string)

	switch kind {
	case "ConfigMap":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	case "Service":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	case "ServiceAccount":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}
	case "ClusterRole":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
	case "ClusterRoleBinding":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
	case "Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	default:
		apiVersion := manifest["apiVersion"].(string)
		parts := strings.Split(apiVersion, "/")
		if len(parts) == 2 {
			return schema.GroupVersionResource{
				Group:    parts[0],
				Version:  parts[1],
				Resource: strings.ToLower(kind) + "s",
			}
		}
		return schema.GroupVersionResource{
			Group:    "",
			Version:  parts[0],
			Resource: strings.ToLower(kind) + "s",
		}
	}
}

func (p *PrometheusProvider) generateManifests(namespace string) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"apiVersion": "v1",
			"kind":       "ServiceAccount",
			"metadata": map[string]interface{}{
				"name":      "prometheus",
				"namespace": namespace,
			},
		},
		{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata": map[string]interface{}{
				"name": "prometheus-kanivet",
			},
			"rules": []interface{}{
				map[string]interface{}{
					"apiGroups": []interface{}{""},
					"resources": []interface{}{"nodes", "nodes/metrics", "nodes/proxy", "services", "endpoints", "pods"},
					"verbs":     []interface{}{"get", "list", "watch"},
				},
				map[string]interface{}{
					"apiGroups": []interface{}{"extensions"},
					"resources": []interface{}{"ingresses"},
					"verbs":     []interface{}{"get", "list", "watch"},
				},
				map[string]interface{}{
					"nonResourceURLs": []interface{}{"/metrics", "/metrics/cadvisor"},
					"verbs":           []interface{}{"get"},
				},
			},
		},
		{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRoleBinding",
			"metadata": map[string]interface{}{
				"name": "prometheus-kanivet",
			},
			"roleRef": map[string]interface{}{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "ClusterRole",
				"name":     "prometheus-kanivet",
			},
			"subjects": []interface{}{
				map[string]interface{}{
					"kind":      "ServiceAccount",
					"name":      "prometheus",
					"namespace": namespace,
				},
			},
		},
		{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "prometheus-config",
				"namespace": namespace,
			},
			"data": map[string]interface{}{
				"prometheus.yml": `global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: 'kubernetes-apiservers'
    kubernetes_sd_configs:
    - role: endpoints
    scheme: https
    tls_config:
      ca_file: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
    relabel_configs:
    - source_labels: [__meta_kubernetes_namespace, __meta_kubernetes_service_name, __meta_kubernetes_endpoint_port_name]
      action: keep
      regex: default;kubernetes;https

  - job_name: 'kubernetes-nodes'
    kubernetes_sd_configs:
    - role: node
    scheme: https
    tls_config:
      ca_file: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
    relabel_configs:
    - action: labelmap
      regex: __meta_kubernetes_node_label_(.+)

  - job_name: 'kubernetes-pods'
    kubernetes_sd_configs:
    - role: pod
    relabel_configs:
    - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
      action: keep
      regex: true
    - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
      action: replace
      target_label: __metrics_path__
      regex: (.+)
    - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
      action: replace
      regex: ([^:]+)(?::\d+)?;(\d+)
      replacement: $1:$2
      target_label: __address__
    - action: labelmap
      regex: __meta_kubernetes_pod_label_(.+)
    - source_labels: [__meta_kubernetes_namespace]
      action: replace
      target_label: kubernetes_namespace
    - source_labels: [__meta_kubernetes_pod_name]
      action: replace
      target_label: kubernetes_pod_name

  - job_name: 'kubernetes-cadvisor'
    kubernetes_sd_configs:
    - role: node
    scheme: https
    tls_config:
      ca_file: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
    relabel_configs:
    - action: labelmap
      regex: __meta_kubernetes_node_label_(.+)
    - target_label: __address__
      replacement: kubernetes.default.svc:443
    - source_labels: [__meta_kubernetes_node_name]
      regex: (.+)
      target_label: __metrics_path__
      replacement: /api/v1/nodes/${1}/proxy/metrics/cadvisor`,
			},
		},
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "prometheus",
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app": "prometheus",
				},
			},
			"spec": map[string]interface{}{
				"replicas": 1,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": "prometheus",
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": "prometheus",
						},
					},
					"spec": map[string]interface{}{
						"serviceAccountName": "prometheus",
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "prometheus",
								"image": "prom/prometheus:v2.45.0",
								"args": []interface{}{
									"--config.file=/etc/prometheus/prometheus.yml",
									"--storage.tsdb.path=/prometheus/",
									"--storage.tsdb.retention.time=7d",
									"--web.console.libraries=/etc/prometheus/console_libraries",
									"--web.console.templates=/etc/prometheus/consoles",
								},
								"ports": []interface{}{
									map[string]interface{}{
										"containerPort": 9090,
										"name":          "web",
									},
								},
								"resources": map[string]interface{}{
									"requests": map[string]interface{}{
										"cpu":    "250m",
										"memory": "512Mi",
									},
									"limits": map[string]interface{}{
										"cpu":    "1000m",
										"memory": "2Gi",
									},
								},
								"volumeMounts": []interface{}{
									map[string]interface{}{
										"name":      "prometheus-config",
										"mountPath": "/etc/prometheus",
									},
									map[string]interface{}{
										"name":      "prometheus-storage",
										"mountPath": "/prometheus",
									},
								},
							},
						},
						"volumes": []interface{}{
							map[string]interface{}{
								"name": "prometheus-config",
								"configMap": map[string]interface{}{
									"name": "prometheus-config",
								},
							},
							map[string]interface{}{
								"name":     "prometheus-storage",
								"emptyDir": map[string]interface{}{},
							},
						},
					},
				},
			},
		},
		{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      "prometheus",
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app": "prometheus",
				},
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"app": "prometheus",
				},
				"ports": []interface{}{
					map[string]interface{}{
						"port":       9090,
						"targetPort": 9090,
						"name":       "web",
					},
				},
			},
		},
	}
}
