package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	natspkg "github.com/kanivet/backend/internal/nats"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *Handler) detectNats(cluster string) *natspkg.Detection {
	cacheKey := h.cache.BuildKey("nats-detect", cluster)
	if v, ok := h.cache.Get(cacheKey); ok {
		if det, ok := v.(*natspkg.Detection); ok && det != nil {
			return det
		}
	}
	meta, metaErr := h.k8s.GetMetadataClient(cluster)
	kube, kubeErr := h.k8s.GetClientForCluster(cluster)
	if metaErr != nil && kubeErr != nil {
		det := &natspkg.Detection{}
		h.cache.Set(cacheKey, det, 10*time.Second)
		return det
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	det, err := natspkg.Detect(ctx, kube, meta)
	if err != nil || det == nil {
		det = &natspkg.Detection{}
		h.cache.Set(cacheKey, det, 10*time.Second)
		return det
	}
	ttl := 10 * time.Minute
	if !det.Installed {
		ttl = 30 * time.Second
	}
	h.cache.Set(cacheKey, det, ttl)
	return det
}

func (h *Handler) buildNatsClient(cluster string) (*natspkg.Client, error) {
	det := h.detectNats(cluster)
	_, cfg, err := h.k8s.GetClientAndConfig(cluster)
	if err != nil {
		return nil, err
	}
	return natspkg.NewClient(cfg, det.Namespace, det.ServiceName, det.MonitorPort), nil
}

func (h *Handler) GetNatsDetection(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	h.respond(c, http.StatusOK, gin.H{"detection": h.detectNats(cluster)}, nil)
}

// natsConnEntry adds the k8s pod a connection came from, resolved by matching
// the connection's IP against the cluster's pod IPs - the monitor endpoint
// only ever reports a bare IP:port, not what's on the other end of it.
type natsConnEntry struct {
	natspkg.ConnInfo
	PodName      string `json:"podName,omitempty"`
	PodNamespace string `json:"podNamespace,omitempty"`
}

type natsConnzView struct {
	NumConnections int             `json:"num_connections"`
	Connections    []natsConnEntry `json:"connections"`
}

// natsRates is computed once per real fetch from the NATS server - never per
// frontend poll, which could be out of phase with the overview cache's TTL
// and alias into a sawtooth (a poll landing on a stale cached sample sees a
// zero delta, the next one sees two intervals' worth at once).
type natsRates struct {
	InMsgsPerSec  float64 `json:"inMsgsPerSec"`
	OutMsgsPerSec float64 `json:"outMsgsPerSec"`
}

type natsOverview struct {
	Varz    *natspkg.Varz    `json:"varz,omitempty"`
	Jsz     *natspkg.Jsz     `json:"jsz,omitempty"`
	Healthz *natspkg.Healthz `json:"healthz,omitempty"`
	Connz   *natsConnzView   `json:"connz,omitempty"`
	Rates   *natsRates       `json:"rates,omitempty"`
	RttMs   *int64           `json:"rttMs,omitempty"`
}

type natsRateSample struct {
	varz *natspkg.Varz
	at   time.Time
}

func (h *Handler) GetNatsOverview(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	det := h.detectNats(cluster)
	if !det.HasMonitor {
		h.respond(c, http.StatusOK, gin.H{"overview": natsOverview{}}, nil)
		return
	}
	cacheKey := h.cache.BuildKey("nats-overview", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 5*time.Second, func() (any, error) {
		return h.fetchNatsOverview(cluster)
	})
	if err != nil {
		h.respond(c, http.StatusBadGateway, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"overview": data}, nil)
}

func (h *Handler) fetchNatsOverview(cluster string) (*natsOverview, error) {
	client, err := h.buildNatsClient(cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	overview := &natsOverview{}
	if varz, err := client.Varz(ctx); err == nil {
		overview.Varz = varz
		overview.Rates = h.computeNatsRates(cluster, varz)
	}
	if jsz, err := client.Jsz(ctx); err == nil {
		overview.Jsz = jsz
	}
	if healthz, err := client.Healthz(ctx); err == nil {
		overview.Healthz = healthz
	}
	if connz, err := client.Connz(ctx); err == nil {
		overview.Connz = h.annotateNatsConnections(ctx, cluster, connz)
	}
	if rtt, ok := h.natsLive.RTTIfConnected(cluster); ok {
		overview.RttMs = new(rtt.Milliseconds())
	}
	return overview, nil
}

// computeNatsRates diffs against the previous real sample for this cluster.
// Called only from inside fetchNatsOverview, i.e. only when the overview
// cache actually expired and we hit the NATS server again - so the elapsed
// time here is the true interval between two real samples, not whatever
// cadence a frontend poller happens to use.
func (h *Handler) computeNatsRates(cluster string, varz *natspkg.Varz) *natsRates {
	cacheKey := h.cache.BuildKey("nats-rate-sample", cluster)
	now := time.Now()
	var rates *natsRates
	if v, ok := h.cache.Get(cacheKey); ok {
		if prev, ok := v.(*natsRateSample); ok && prev != nil {
			elapsedSec := now.Sub(prev.at).Seconds()
			if elapsedSec > 0 {
				rates = &natsRates{
					InMsgsPerSec:  max(0, float64(varz.InMsgs-prev.varz.InMsgs)/elapsedSec),
					OutMsgsPerSec: max(0, float64(varz.OutMsgs-prev.varz.OutMsgs)/elapsedSec),
				}
			}
		}
	}
	h.cache.Set(cacheKey, &natsRateSample{varz: varz, at: now}, time.Minute)
	return rates
}

// annotateNatsConnections resolves each connection's IP to the pod it belongs
// to, so "some client at 10.42.1.7" becomes "my-service-7d9f8b7f9c-x2kdp".
func (h *Handler) annotateNatsConnections(ctx context.Context, cluster string, connz *natspkg.Connz) *natsConnzView {
	view := &natsConnzView{
		NumConnections: connz.NumConnections,
		Connections:    make([]natsConnEntry, len(connz.Connections)),
	}
	podsByIP := h.natsPodsByIP(ctx, cluster)
	for i, conn := range connz.Connections {
		entry := natsConnEntry{ConnInfo: conn}
		if pod, ok := podsByIP[conn.IP]; ok {
			entry.PodName = pod.name
			entry.PodNamespace = pod.namespace
		}
		view.Connections[i] = entry
	}
	return view
}

type natsPodRef struct{ name, namespace string }

func (h *Handler) natsPodsByIP(ctx context.Context, cluster string) map[string]natsPodRef {
	cacheKey := h.cache.BuildKey("nats-pods-by-ip", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 5*time.Second, func() (any, error) {
		kube, err := h.k8s.GetClientForCluster(cluster)
		if err != nil {
			return map[string]natsPodRef{}, nil
		}
		pods, err := kube.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return map[string]natsPodRef{}, nil
		}
		byIP := make(map[string]natsPodRef, len(pods.Items))
		for _, pod := range pods.Items {
			if pod.Status.PodIP != "" {
				byIP[pod.Status.PodIP] = natsPodRef{name: pod.Name, namespace: pod.Namespace}
			}
		}
		return byIP, nil
	})
	if err != nil {
		return map[string]natsPodRef{}
	}
	byIP, _ := data.(map[string]natsPodRef)
	return byIP
}
