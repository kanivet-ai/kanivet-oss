package metrics

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Provider detection used to trust Service objects: if something called
// "prometheus" existed, it was "found" and cached for half an hour. That is how
// a Service that had been pointing at nothing for two months kept every chart
// stuck on "port forward failed to start". Detection now has three steps —
// classify Services, check that ready pods stand behind them, and ask the
// Prometheus-compatible API to identify itself through a short port-forward —
// and only a candidate that passes all three is reported as found.

// detectPositiveTTL is how long a verified provider is remembered. A verified
// provider is stable; the cache is dropped early anyway when a query later
// finds no pod behind it.
const detectPositiveTTL = 30 * time.Minute

// detectNegativeTTL is how long "nothing found" is remembered. It has to be
// short or a Prometheus installed (or a pod that became Ready) a minute ago
// stays invisible until Kanivet restarts. A var so tests can shrink it.
var detectNegativeTTL = 45 * time.Second

// maxVerifiedCandidates bounds how many Services a single detection pass will
// port-forward into. Candidates are tried best-first, so this only matters in
// clusters full of broken look-alikes.
const maxVerifiedCandidates = 4

type detectEntry struct {
	info    *ProviderInfo
	expires time.Time
}

// detectCached runs fetch under the cache's single-flight and remembers the
// answer for a time that depends on the outcome: found → detectPositiveTTL,
// not found → detectNegativeTTL. Errors are never cached.
func detectCached(c *cache.Cache, key string, fetch func() (*ProviderInfo, error)) (*ProviderInfo, error) {
	for attempt := 0; attempt < 2; attempt++ {
		data, err := c.GetOrSet(key, detectPositiveTTL, func() (interface{}, error) {
			info, err := fetch()
			if err != nil {
				return nil, err
			}
			ttl := detectPositiveTTL
			if !info.Found {
				ttl = detectNegativeTTL
			}
			return &detectEntry{info: info, expires: time.Now().Add(ttl)}, nil
		})
		if err != nil {
			return nil, err
		}
		entry, ok := data.(*detectEntry)
		if ok && time.Now().Before(entry.expires) {
			return entry.info, nil
		}
		c.Delete(key)
	}
	return fetch()
}

// serviceBackends is what stands behind a Service right now.
type serviceBackends struct {
	Ready    int
	Total    int
	ReadyPod string // one ready pod we can port-forward to
	err      error  // set only when nothing could be inspected (RBAC, API down)
}

func (b serviceBackends) describe() string {
	switch {
	case b.err != nil:
		return "could not inspect its endpoints: " + trimErr(b.err)
	case b.Total == 0:
		return "no pods behind the service"
	case b.Ready == 0:
		return fmt.Sprintf("%d pod(s), none ready", b.Total)
	case b.ReadyPod == "":
		return fmt.Sprintf("%d ready endpoint(s), none of them a pod", b.Ready)
	default:
		return fmt.Sprintf("%d of %d ready", b.Ready, b.Total)
	}
}

// listServiceBackends reads the EndpointSlices of svc — falling back to the
// legacy Endpoints object and then to pods matching the selector — and reports
// how many endpoints are ready plus one ready pod to port-forward to.
func listServiceBackends(ctx context.Context, clientset kubernetes.Interface, svc *v1.Service) serviceBackends {
	var out serviceBackends

	slices, slicesErr := clientset.DiscoveryV1().EndpointSlices(svc.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubernetes.io/service-name=" + svc.Name,
	})
	if slicesErr == nil && len(slices.Items) > 0 {
		for _, slice := range slices.Items {
			for _, ep := range slice.Endpoints {
				out.Total++
				if ep.Conditions.Ready != nil && !*ep.Conditions.Ready {
					continue
				}
				out.Ready++
				if out.ReadyPod == "" && ep.TargetRef != nil && ep.TargetRef.Kind == "Pod" {
					out.ReadyPod = ep.TargetRef.Name
				}
			}
		}
		return out
	}

	eps, epErr := clientset.CoreV1().Endpoints(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
	if epErr == nil {
		for _, subset := range eps.Subsets {
			out.Total += len(subset.Addresses) + len(subset.NotReadyAddresses)
			out.Ready += len(subset.Addresses)
			for _, addr := range subset.Addresses {
				if out.ReadyPod == "" && addr.TargetRef != nil && addr.TargetRef.Kind == "Pod" {
					out.ReadyPod = addr.TargetRef.Name
				}
			}
		}
		if out.Total > 0 {
			return out
		}
	}

	if len(svc.Spec.Selector) > 0 {
		pods, podErr := clientset.CoreV1().Pods(svc.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector}),
			Limit:         20,
		})
		if podErr == nil {
			for i := range pods.Items {
				pod := &pods.Items[i]
				out.Total++
				if pod.Status.Phase == v1.PodRunning && isPodReady(pod) {
					out.Ready++
					if out.ReadyPod == "" {
						out.ReadyPod = pod.Name
					}
				}
			}
			return out
		}
		if slicesErr != nil && epErr != nil {
			out.err = podErr
		}
		return out
	}

	if slicesErr != nil && epErr != nil {
		out.err = slicesErr
	}
	return out
}

func isPodReady(pod *v1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == v1.PodReady {
			return c.Status == v1.ConditionTrue
		}
	}
	return false
}

// pickServicePort chooses the ServicePort we will talk to: a well-known port
// number first, then a well-known port name, then whatever comes first.
func pickServicePort(ports []v1.ServicePort, numbers []int32, names []string) (v1.ServicePort, bool) {
	for _, n := range numbers {
		for _, sp := range ports {
			if sp.Port == n || sp.TargetPort.IntVal == n {
				return sp, true
			}
		}
	}
	for _, name := range names {
		for _, sp := range ports {
			if strings.EqualFold(sp.Name, name) {
				return sp, true
			}
		}
	}
	if len(ports) > 0 {
		return ports[0], true
	}
	return v1.ServicePort{}, false
}

// resolvePodPort turns a ServicePort into the port the pod actually listens
// on. Port-forwarding talks to the pod, so a Service on :80 whose targetPort
// is the named container port "http-metrics" must resolve to 8080, not 80 —
// getting this wrong is what made earlier versions rebuild the tunnel on every
// query.
func resolvePodPort(ctx context.Context, clientset kubernetes.Interface, namespace, podName string, sp v1.ServicePort, fallback int32) int32 {
	if sp.TargetPort.IntVal > 0 {
		return sp.TargetPort.IntVal
	}
	if sp.TargetPort.StrVal != "" && podName != "" {
		if pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{}); err == nil {
			for _, container := range pod.Spec.Containers {
				for _, port := range container.Ports {
					if port.Name == sp.TargetPort.StrVal {
						return port.ContainerPort
					}
				}
			}
		}
	}
	if sp.Port > 0 {
		return sp.Port
	}
	return fallback
}

// probeOutcome is what a short port-forwarded request to the Prometheus API
// told us about a candidate.
type probeOutcome struct {
	OK          bool
	NeedsTenant bool // 401: a multi-tenant gateway wants X-Scope-OrgID
	Version     string
	Detail      string // why it is not OK, in plain words
}

// probePrometheusAPI opens a short-lived port-forward to pod:port and asks the
// Prometheus-compatible API under basePath to identify itself. It is the
// ground truth for "found".
func probePrometheusAPI(k8sClient k8s.Interface, cluster, namespace, pod string, port int32, basePath string, headers map[string]string) probeOutcome {
	pf, err := k8sClient.CreatePortForward(cluster, namespace, pod, int(port))
	if err != nil {
		return probeOutcome{Detail: fmt.Sprintf("port-forward to pod %s:%d failed: %s", pod, port, trimErr(err))}
	}
	defer func() { _ = k8sClient.StopPortForward(pf.ID) }()

	client := &http.Client{Timeout: 4 * time.Second}
	base := fmt.Sprintf("http://localhost:%d%s", pf.LocalPort, strings.TrimSuffix(basePath, "/"))

	// buildinfo carries the version; labels is the fallback for proxies that
	// only route the query endpoints.
	var last probeOutcome
	for _, path := range []string{"/api/v1/status/buildinfo", "/api/v1/labels"} {
		last = probeOnce(client, base+path, headers)
		if last.OK || last.NeedsTenant {
			return last
		}
	}
	return last
}

func probeOnce(client *http.Client, url string, headers map[string]string) probeOutcome {
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		req, reqErr := http.NewRequest(http.MethodGet, url, nil)
		if reqErr != nil {
			return probeOutcome{Detail: reqErr.Error()}
		}
		req.Header.Set("Accept", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		// A freshly opened tunnel sometimes drops the very first request.
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		return probeOutcome{Detail: "no answer through the port-forward: " + trimErr(err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return probeOutcome{NeedsTenant: true, Detail: "needs a tenant (X-Scope-OrgID) or credentials"}
	case resp.StatusCode == http.StatusForbidden:
		return probeOutcome{Detail: "refused the request (403)"}
	case resp.StatusCode == http.StatusNotFound:
		return probeOutcome{Detail: "does not serve the Prometheus API at this path (404)"}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return probeOutcome{Detail: fmt.Sprintf("answered HTTP %d", resp.StatusCode)}
	}

	var payload map[string]any
	if jsonv2.Unmarshal(body, &payload) != nil || payload["status"] != "success" {
		return probeOutcome{Detail: "answered, but not with the Prometheus API"}
	}
	outcome := probeOutcome{OK: true}
	if data, ok := payload["data"].(map[string]any); ok {
		if version, ok := data["version"].(string); ok {
			outcome.Version = version
		}
	}
	return outcome
}

// rankMonitoringNamespace scores a namespace by how likely it is to host the
// canonical metrics store. Conventional monitoring namespaces dominate so a
// same-name candidate in one of them always beats a stray copy elsewhere.
func rankMonitoringNamespace(ns string) int {
	switch {
	case ns == "monitoring":
		return 100
	case ns == "prometheus", ns == "kube-prometheus-stack", ns == "kube-prometheus":
		return 95
	case ns == "observability", ns == "mimir", ns == "cortex", ns == "thanos", ns == "victoria-metrics", ns == "metrics":
		return 90
	case strings.Contains(ns, "monitor"):
		return 60
	case strings.Contains(ns, "prom"), strings.Contains(ns, "observ"), strings.Contains(ns, "metric"), strings.Contains(ns, "mimir"), strings.Contains(ns, "cortex"):
		return 50
	default:
		// Slightly below zero so an unknown namespace never ties with a known one.
		return -10
	}
}

// bundledNamespaces host metrics stores that belong to another tool (short
// retention, partial scrape coverage). They stay eligible — better than
// nothing — but rank below anything a person installed on purpose.
var bundledNamespaces = []string{"opencost", "kubecost", "loki", "tempo", "grafana-cloud", "pyroscope"}

func isBundledNamespace(ns string) bool {
	for _, bn := range bundledNamespaces {
		if ns == bn || strings.HasPrefix(ns, bn+"-") {
			return true
		}
	}
	return false
}

func lowerLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[strings.ToLower(k)] = strings.ToLower(v)
	}
	return out
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func joinReasons(reasons []string, none string) string {
	if len(reasons) == 0 {
		return none
	}
	return strings.Join(reasons, "; ")
}

// trimErr keeps error text readable in the UI: one line, no URL dumps.
func trimErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
	if len(msg) > 160 {
		msg = msg[:157] + "…"
	}
	return msg
}
