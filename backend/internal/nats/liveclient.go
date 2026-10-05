// Tier 2: a real NATS client connection, used only by the features that need
// message content or live subscriptions (monitor HTTP cannot provide either).
// Every call path in this file and its siblings (livetail.go, etc.) is
// read-only by construction: no Publish, no stream/consumer/KV/object-store
// create/edit/delete/purge, no consumer ack/nak/term. A code reviewer should
// treat a write or mutate call appearing anywhere in this package as a bug.
package nats

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	clientPortName    = "nats"
	defaultClientPort = int32(4222)
	negativeCacheTTL  = 30 * time.Second
)

// liveConn is one pooled, real NATS connection for a cluster.
type liveConn struct {
	nc       *natsgo.Conn
	js       jetstream.JetStream
	pf       *k8s.PortForward
	cred     resolvedCredential
	lastUsed time.Time
}

type negativeEntry struct {
	until  time.Time
	reason string
}

// LivePool holds at most one live NATS connection per cluster, opened lazily
// on first use and closed after a period of inactivity. Tier 1 (the
// monitor-HTTP-only read-only dashboard) never touches this pool.
type LivePool struct {
	mu    sync.Mutex
	k8s   k8s.Interface
	conns map[string]*liveConn
	neg   map[string]negativeEntry
}

func NewLivePool(kube k8s.Interface) *LivePool {
	return &LivePool{
		k8s:   kube,
		conns: make(map[string]*liveConn),
		neg:   make(map[string]negativeEntry),
	}
}

// Get returns a live connection for cluster, reusing a cached one when
// healthy, or opening a new one. Callers never see a port-forward or a NATS
// credential; both are internal to this pool.
func (p *LivePool) Get(ctx context.Context, cluster string) (*liveConn, error) {
	if lc, ok := p.cached(cluster); ok {
		return lc, nil
	}
	// Checked here, once, so a cooldown never becomes next attempt's stored
	// reason - connect() below always either succeeds or returns a fresh,
	// unnested error.
	if until, reason, waiting := p.negative(cluster); waiting {
		return nil, fmt.Errorf("NATS live connection unavailable, retry after %s: %s", time.Until(until).Round(time.Second), reason)
	}

	lc, err := p.connect(ctx, cluster)
	if err != nil {
		p.mu.Lock()
		p.neg[cluster] = negativeEntry{until: time.Now().Add(negativeCacheTTL), reason: err.Error()}
		p.mu.Unlock()
		return nil, err
	}
	return p.store(cluster, lc), nil
}

func (p *LivePool) cached(cluster string) (*liveConn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if lc, ok := p.conns[cluster]; ok {
		if lc.nc.IsConnected() {
			lc.lastUsed = time.Now()
			return lc, true
		}
		p.closeLocked(cluster)
	}
	return nil, false
}

// RTTIfConnected reports the round-trip latency of an already-open live
// connection for cluster. It never opens a new connection: the overview
// panel shows RTT only as a bonus once some other tier-2 feature already
// justified a live connection, not as a reason to open one on its own.
func (p *LivePool) RTTIfConnected(cluster string) (time.Duration, bool) {
	lc, ok := p.cached(cluster)
	if !ok {
		return 0, false
	}
	rtt, err := lc.nc.RTT()
	if err != nil {
		return 0, false
	}
	return rtt, true
}

// store wins a race against a concurrent connect for the same cluster by
// keeping whichever connection is already cached, and closing the other one
// so a port-forward is never leaked.
func (p *LivePool) store(cluster string, lc *liveConn) *liveConn {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.conns[cluster]; ok && existing.nc.IsConnected() {
		existing.lastUsed = time.Now()
		lc.nc.Close()
		if lc.pf != nil {
			_ = p.k8s.StopPortForward(lc.pf.ID)
		}
		return existing
	}
	p.conns[cluster] = lc
	delete(p.neg, cluster)
	return lc
}

func (p *LivePool) connect(ctx context.Context, cluster string) (*liveConn, error) {
	kube, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("get cluster client: %w", err)
	}
	meta, err := p.k8s.GetMetadataClient(cluster)
	if err != nil {
		return nil, fmt.Errorf("get metadata client: %w", err)
	}

	det, err := Detect(ctx, kube, meta)
	if err != nil || !det.HasMonitor {
		return nil, fmt.Errorf("NATS not detected on this cluster")
	}

	svc, err := kube.CoreV1().Services(det.Namespace).Get(ctx, det.ServiceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get NATS service %s/%s: %w", det.Namespace, det.ServiceName, err)
	}
	clientPort := findClientPort(svc)
	if clientPort == 0 {
		return nil, fmt.Errorf("no client port found on NATS service %s/%s", det.Namespace, det.ServiceName)
	}

	podName, targetPort, err := k8s.ResolveServicePod(ctx, kube, det.Namespace, det.ServiceName, clientPort)
	if err != nil {
		return nil, fmt.Errorf("resolve pod for NATS service: %w", err)
	}

	pf, err := p.k8s.CreatePortForward(cluster, det.Namespace, podName, targetPort)
	if err != nil {
		return nil, fmt.Errorf("port-forward to NATS pod: %w", err)
	}

	// Best-effort: a dynamic client only improves credential discovery (it
	// lets resolveCredential follow NatsAccount CRs to other namespaces), so
	// a failure here doesn't block connecting - it just narrows the search.
	dyn, _ := p.k8s.GetDynamicClient(cluster)

	url := fmt.Sprintf("127.0.0.1:%d", pf.LocalPort)
	nc, cred, err := resolveCredential(ctx, kube, dyn, det.Namespace, url)
	if err != nil {
		_ = p.k8s.StopPortForward(pf.ID)
		return nil, err
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		_ = p.k8s.StopPortForward(pf.ID)
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}

	return &liveConn{nc: nc, js: js, pf: pf, cred: cred, lastUsed: time.Now()}, nil
}

func (p *LivePool) negative(cluster string) (until time.Time, reason string, waiting bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.neg[cluster]
	if !ok || time.Now().After(entry.until) {
		return time.Time{}, "", false
	}
	return entry.until, entry.reason, true
}

// closeLocked closes and forgets a cached connection. Callers must hold p.mu.
func (p *LivePool) closeLocked(cluster string) {
	lc, ok := p.conns[cluster]
	if !ok {
		return
	}
	lc.nc.Close()
	if lc.pf != nil {
		_ = p.k8s.StopPortForward(lc.pf.ID)
	}
	delete(p.conns, cluster)
}

// Close tears down the cached connection for cluster, if any. Safe to call
// when no connection is cached.
func (p *LivePool) Close(cluster string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked(cluster)
}

// RunIdleSweep closes any connection idle longer than maxIdle, checking every
// interval, until ctx is done. Meant to run for the life of the process.
func (p *LivePool) RunIdleSweep(ctx context.Context, interval, maxIdle time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.sweepOnce(maxIdle)
		}
	}
}

func (p *LivePool) sweepOnce(maxIdle time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-maxIdle)
	for cluster, lc := range p.conns {
		if lc.lastUsed.Before(cutoff) {
			p.closeLocked(cluster)
		}
	}
}

func findClientPort(svc *corev1.Service) int32 {
	for _, port := range svc.Spec.Ports {
		if port.Name == clientPortName {
			return port.Port
		}
	}
	for _, port := range svc.Spec.Ports {
		if port.Port == defaultClientPort {
			return port.Port
		}
	}
	return 0
}
