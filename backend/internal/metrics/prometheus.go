package metrics

import (
	"context"
	"errors"
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
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type PrometheusProvider struct {
	k8s             k8s.Interface
	cache           *cache.Cache
	queries         *queryCache // chart answers only, never history
	portForwardPool sync.Map    // cluster -> *PortForwardInfo
	recreateState   sync.Map    // cluster -> *recreateTracker
}

type PortForwardInfo struct {
	PortForward *k8s.PortForward
	HTTPClient  *http.Client
	LastUsed    time.Time
	Mutex       sync.Mutex
	// BasePath is the URL prefix the Prometheus API lives under: "" for
	// Prometheus/Thanos, "/prometheus" for Mimir, "/select/0/prometheus" for
	// VictoriaMetrics vmselect.
	BasePath string
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
	if IsConnRefused(err) || IsConnReset(err) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
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

// prometheusPoolKey identifies a port-forward by the Service it reaches, so a
// provider change (another namespace/service after re-detection) never reuses
// a tunnel into the old pod.
func prometheusPoolKey(cluster string, info *ProviderInfo) string {
	return cluster + "|" + info.Namespace + "/" + info.Service
}

// forgetDetection drops the cached provider so the next query re-detects.
func (p *PrometheusProvider) forgetDetection(cluster string) {
	p.cache.Delete(p.cache.BuildKey("prometheus-info", cluster))
}

// newPortForwardInfo wraps a tunnel with the HTTP client queries use.
func newPortForwardInfo(pf *k8s.PortForward, basePath string) *PortForwardInfo {
	httpClient := &http.Client{
		Timeout: 5 * time.Second, // Reduced for faster failure detection
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			// History answers run to megabytes over a loopback port-forward:
			// the default 4KB buffer costs one read syscall per 4KB.
			ReadBufferSize:     64 << 10,
			DisableCompression: false,
			DisableKeepAlives:  false,
			// Fast failure on connection issues
			DialContext: (&net.Dialer{
				Timeout:   1 * time.Second, // Fast connection timeout
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
	return &PortForwardInfo{PortForward: pf, HTTPClient: httpClient, LastUsed: time.Now(), BasePath: basePath}
}

// tunnelAlive reports whether a pooled tunnel is still running. The manager
// forgets a forward the moment it exits (pod restart, network drop), so this
// is exact, and cheap enough to ask before every query.
func tunnelAlive(client k8s.Interface, pfInfo *PortForwardInfo) bool {
	cur, ok := client.GetPortForward(pfInfo.PortForward.ID)
	return ok && cur == pfInfo.PortForward
}

// pooledPortForward returns the live tunnel pooled under key. A dead one is
// dropped, so the caller opens a new one now instead of failing queries on it
// until the recreate throttle allows a rebuild.
func pooledPortForward(pool *sync.Map, client k8s.Interface, key string) *PortForwardInfo {
	v, ok := pool.Load(key)
	if !ok {
		return nil
	}
	pfInfo := v.(*PortForwardInfo)
	if !tunnelAlive(client, pfInfo) {
		pool.CompareAndDelete(key, pfInfo)
		return nil
	}
	pfInfo.Mutex.Lock()
	pfInfo.LastUsed = time.Now()
	pfInfo.Mutex.Unlock()
	return pfInfo
}

// poolPortForward pools pfInfo under key and returns what the pool holds
// afterwards. When a live tunnel got there first (a concurrent query, or the
// pool's own tunnel when detection runs again) that one is kept and pfInfo's
// is stopped: tunnels are private, so one left out of the pool would never be
// reaped.
func poolPortForward(pool *sync.Map, client k8s.Interface, key string, pfInfo *PortForwardInfo) *PortForwardInfo {
	for {
		cur, loaded := pool.LoadOrStore(key, pfInfo)
		if !loaded {
			return pfInfo
		}
		existing := cur.(*PortForwardInfo)
		if tunnelAlive(client, existing) {
			_ = client.StopPortForward(pfInfo.PortForward.ID)
			return existing
		}
		if pool.CompareAndSwap(key, existing, pfInfo) {
			_ = client.StopPortForward(existing.PortForward.ID)
			return pfInfo
		}
	}
}

// Get or create a port forward for the detected provider
func (p *PrometheusProvider) getOrCreatePortForward(cluster string, promInfo *ProviderInfo) (*PortForwardInfo, error) {
	key := prometheusPoolKey(cluster, promInfo)
	if pfInfo := pooledPortForward(&p.portForwardPool, p.k8s, key); pfInfo != nil {
		return pfInfo, nil
	}

	podName := p.findPrometheusPod(cluster, promInfo)
	if podName == "" {
		// The Service we detected has nothing ready behind it any more. Forget
		// the detection so the next query re-detects instead of failing on an
		// empty pod name until the cache expires.
		p.forgetDetection(cluster)
		return nil, fmt.Errorf("no ready pod behind %s/%s", promInfo.Namespace, promInfo.Service)
	}

	targetPort := promInfo.Port
	if targetPort == 0 {
		targetPort = 9090
	}
	// CreatePrivatePortForward returns once the local listener is bound, so
	// the tunnel is usable straight away.
	pf, err := p.k8s.CreatePrivatePortForward(cluster, promInfo.Namespace, podName, int(targetPort))
	if err != nil {
		p.forgetDetection(cluster)
		return nil, fmt.Errorf("failed to create port forward: %w", err)
	}
	return poolPortForward(&p.portForwardPool, p.k8s, key, newPortForwardInfo(pf, promInfo.Path)), nil
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
				p.portForwardPool.CompareAndDelete(key, pfInfo)
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
			// Read under the lock, ask without it: queries take the same lock
			// to stamp LastUsed and must not wait out a slow health check.
			pfInfo.Mutex.Lock()
			recent := time.Since(pfInfo.LastUsed) < 5*time.Minute
			healthURL := fmt.Sprintf("http://localhost:%d%s/api/v1/status/buildinfo", pfInfo.PortForward.LocalPort, pfInfo.BasePath)
			pfInfo.Mutex.Unlock()

			// Only send keepalive if the port forward was used recently (within last 5 minutes)
			if recent {
				// Send a lightweight health check to keep the connection alive
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
			return true
		})
	}
}

func (p *PrometheusProvider) GetName() string {
	return "prometheus"
}

func (p *PrometheusProvider) Detect(cluster string) (*ProviderInfo, error) {
	return detectCached(p.cache, p.cache.BuildKey("prometheus-info", cluster), func() (*ProviderInfo, error) {
		return p.detectInternal(cluster)
	})
}

// detectInternal lists every Service in the cluster, keeps the ones that look
// like a Prometheus-compatible store (Prometheus, Thanos Query,
// VictoriaMetrics) and verifies them best-first: ready pods behind the
// Service, then a probe of the API through a port-forward. The first one that
// answers is the provider. If none does, the reasons travel back so the UI can
// say why instead of just "not detected".
func (p *PrometheusProvider) detectInternal(cluster string) (*ProviderInfo, error) {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// One call for every Service, served from the apiserver watch cache.
	services, err := clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	candidates := prometheusCandidates(services.Items)
	if len(candidates) == 0 {
		return &ProviderInfo{Type: "prometheus", Found: false, Reason: "no Prometheus, Thanos or VictoriaMetrics service in this cluster"}, nil
	}
	if len(candidates) > 1 {
		names := make([]string, 0, len(candidates))
		for _, c := range candidates {
			names = append(names, fmt.Sprintf("%s/%s(%d)", c.svc.Namespace, c.svc.Name, c.score))
		}
		log.Printf("[Prometheus] %d candidates in cluster %s: %s", len(candidates), cluster, strings.Join(names, ", "))
	}

	var reasons []string
	for i := range candidates {
		if i >= maxVerifiedCandidates {
			break
		}
		c := &candidates[i]
		where := c.svc.Namespace + "/" + c.svc.Name

		backends := listServiceBackends(ctx, clientset, &c.svc)
		if backends.ReadyPod == "" {
			reasons = append(reasons, where+": "+backends.describe())
			continue
		}

		port := resolvePodPort(ctx, clientset, c.svc.Namespace, backends.ReadyPod, c.svcPort, c.defaultPort)
		outcome, tunnel := probePrometheusAPI(p.k8s, cluster, c.svc.Namespace, backends.ReadyPod, port, c.path, nil)
		if !outcome.OK {
			reasons = append(reasons, where+": "+outcome.Detail)
			continue
		}

		info := &ProviderInfo{
			Type:      "prometheus",
			Found:     true,
			Verified:  true,
			Flavor:    c.flavor,
			Namespace: c.svc.Namespace,
			Service:   c.svc.Name,
			Port:      port,
			Path:      c.path,
			Version:   outcome.Version,
			URL:       fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s", c.svc.Name, c.svc.Namespace, c.svcPort.Port, c.path),
		}
		log.Printf("[Prometheus] Using %s %s (pod %s:%d, version %q) in cluster %s", c.flavor, where, backends.ReadyPod, port, outcome.Version, cluster)
		// The first chart usually follows detection at once: give it the
		// probe's tunnel rather than looking the pod up and dialling again.
		// Idle cleanup reclaims it if no chart comes.
		poolPortForward(&p.portForwardPool, p.k8s, prometheusPoolKey(cluster, info), newPortForwardInfo(tunnel, info.Path))
		return info, nil
	}

	reason := joinReasons(reasons, "no Prometheus-compatible service could be verified")
	log.Printf("[Prometheus] No usable provider in cluster %s: %s", cluster, reason)
	return &ProviderInfo{Type: "prometheus", Found: false, Reason: reason}, nil
}

// promCandidate is a Service that looks like it serves the Prometheus HTTP API.
type promCandidate struct {
	svc         v1.Service
	svcPort     v1.ServicePort
	defaultPort int32
	flavor      string // prometheus | thanos | victoriametrics
	path        string // URL prefix the API lives under
	score       int
}

// prometheusCandidates classifies and ranks Services, best first. Ranking is
// only an order of verification: a candidate still has to have ready pods and
// answer the API before it is reported as found.
func prometheusCandidates(services []v1.Service) []promCandidate {
	var out []promCandidate
	for _, svc := range services {
		if c, ok := classifyPrometheusService(svc); ok {
			out = append(out, c)
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

// promExcludedFragments mark Services that carry "prometheus"/"thanos" in
// their name or labels but are not a queryable TSDB: alerting, exporters,
// shippers, operators, sidecars and the storage-side Thanos components.
var promExcludedFragments = []string{
	"alertmanager", "pushgateway", "operator", "exporter", "adapter", "karma",
	"blackbox", "snmp", "statsd", "msteams", "config-reloader", "webhook",
	"grafana-agent", "alloy", "otel", "collector", "kube-state-metrics",
	"metrics-server", "sidecar", "compact", "thanos-store", "store-gateway",
	"storegateway", "receive", "thanos-rule", "ruler", "bucket", "-agent", "agent-",
}

// classifyPrometheusService decides whether a Service is worth verifying as a
// Prometheus-compatible store, which flavour it is, which of its ports to talk
// to and under which URL prefix, and how it ranks against other candidates.
//
// Names and labels are matched together because helm charts and operators
// disagree about where the identifying word ends up. Well-known monitoring
// namespaces dominate the score so a same-name candidate in one of them always
// beats a stray copy elsewhere; stores bundled with another tool (opencost,
// kubecost, …) stay eligible but rank last.
func classifyPrometheusService(svc v1.Service) (promCandidate, bool) {
	name := strings.ToLower(svc.Name)
	ns := strings.ToLower(svc.Namespace)
	labels := lowerLabels(svc.Labels)
	haystack := strings.Join([]string{
		name,
		labels["app.kubernetes.io/name"],
		labels["app.kubernetes.io/component"],
		labels["app"],
		labels["component"],
	}, " ")

	for _, frag := range promExcludedFragments {
		if strings.Contains(haystack, frag) {
			return promCandidate{}, false
		}
	}

	c := promCandidate{svc: svc}
	ports := svc.Spec.Ports
	switch {
	case strings.Contains(haystack, "thanos"):
		// Only Thanos Query / Query Frontend speak the Prometheus read API.
		if !strings.Contains(haystack, "quer") {
			return promCandidate{}, false
		}
		c.flavor = "thanos"
		c.defaultPort = 10902
		c.svcPort, _ = pickServicePort(ports, []int32{9090, 10902}, []string{"http", "web"})
		c.score += 10
		if strings.Contains(haystack, "frontend") {
			c.score += 5
		}
	case strings.Contains(haystack, "vmselect"):
		c.flavor = "victoriametrics"
		c.path = "/select/0/prometheus"
		c.defaultPort = 8481
		c.svcPort, _ = pickServicePort(ports, []int32{8481}, []string{"http"})
		c.score += 20
	case containsAny(haystack, "vminsert", "vmstorage", "vmagent", "vmalert", "vmauth", "vmbackup", "vmrestore"):
		return promCandidate{}, false
	case containsAny(haystack, "vmsingle", "victoria-metrics", "victoriametrics"):
		c.flavor = "victoriametrics"
		c.defaultPort = 8428
		c.svcPort, _ = pickServicePort(ports, []int32{8428}, []string{"http"})
		c.score += 20
	case strings.Contains(haystack, "prometheus") || labels["operated-prometheus"] == "true":
		c.flavor = "prometheus"
		c.defaultPort = 9090
		c.svcPort, _ = pickServicePort(ports, []int32{9090}, []string{"web", "http-web", "http"})
	default:
		return promCandidate{}, false
	}

	c.score += rankMonitoringNamespace(ns) + rankPrometheusName(name)
	if isBundledNamespace(ns) {
		c.score -= 100
	}
	// prometheus-operated is headless and canonical; for the other flavours a
	// headless twin of the ClusterIP service ranks just behind it.
	if svc.Spec.ClusterIP == v1.ClusterIPNone && c.flavor != "prometheus" {
		c.score -= 5
	}
	return c, true
}

// rankPrometheusName prefers the canonical service names produced by the
// upstream helm charts and operators.
func rankPrometheusName(name string) int {
	switch {
	case name == "prometheus-operated":
		return 50 // prometheus-operator's headless service for the Prometheus CRD
	case strings.Contains(name, "kube-prometheus-stack-prometheus"):
		return 45
	case strings.Contains(name, "kube-prometheus-prometheus"), name == "prometheus-k8s":
		return 40
	case strings.Contains(name, "prometheus-server"):
		return 30
	case name == "prometheus":
		return 25
	default:
		return 10
	}
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

// chartGet runs a chart's range query through the chart cache. Charts have
// no retry logic of their own, so a query that found its port-forward dead is
// asked once more over the fresh one; history queries leave that to
// rightsizing's retry budget.
func (p *PrometheusProvider) chartGet(ctx context.Context, cluster string, params url.Values) (io.ReadCloser, error) {
	info, err := p.Detect(cluster)
	if err != nil || !info.Found {
		return nil, fmt.Errorf("prometheus not found in cluster: %w", ErrNoHistorySource)
	}
	return p.queries.prom(ctx, querySource(cluster, info, ""), "/api/v1/query_range", params, func(params url.Values) (io.ReadCloser, error) {
		body, err := p.promGet(ctx, cluster, "/api/v1/query_range", params)
		if errors.Is(err, errPortForwardDropped) {
			body, err = p.promGet(ctx, cluster, "/api/v1/query_range", params)
		}
		return body, err
	})
}

func (p *PrometheusProvider) QueryMetrics(ctx context.Context, cluster string, query MetricQuery) (*MetricResponse, error) {
	return queryChart(ctx, p, "prometheus", cluster, query)
}

// QueryWorkloadMetrics charts several pods with one query, one series per pod.
func (p *PrometheusProvider) QueryWorkloadMetrics(ctx context.Context, cluster string, query WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	return queryWorkloadChart(ctx, p, "prometheus", cluster, query)
}

// findPrometheusPod returns a ready pod behind the detected Service. When the
// Service has no selector (manual endpoints) it falls back to any ready pod in
// the namespace that runs a Prometheus-like image.
func (p *PrometheusProvider) findPrometheusPod(cluster string, info *ProviderInfo) string {
	clientset, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if info.Service != "" {
		if svc, err := clientset.CoreV1().Services(info.Namespace).Get(ctx, info.Service, metav1.GetOptions{}); err == nil {
			if backends := listServiceBackends(ctx, clientset, svc); backends.ReadyPod != "" {
				return backends.ReadyPod
			}
			if len(svc.Spec.Selector) > 0 {
				return "" // the Service knows its pods and none of them is ready
			}
		}
	}

	pods, err := clientset.CoreV1().Pods(info.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != v1.PodRunning || !isPodReady(pod) {
			continue
		}
		for _, container := range pod.Spec.Containers {
			if containsAny(strings.ToLower(container.Image), "prometheus", "thanos", "victoria") {
				return pod.Name
			}
		}
	}
	return ""
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
