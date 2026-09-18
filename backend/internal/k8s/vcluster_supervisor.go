package k8s

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type VClusterState string

const (
	VClusterStateConnecting   VClusterState = "connecting"
	VClusterStateHealthy      VClusterState = "healthy"
	VClusterStateReconnecting VClusterState = "reconnecting"
	VClusterStateFailed       VClusterState = "failed"
)

type VClusterStatus struct {
	ID         string        `json:"id"`
	State      VClusterState `json:"state"`
	Generation uint64        `json:"generation"`
	LocalPort  int           `json:"localPort,omitempty"`
	Detail     string        `json:"detail,omitempty"`
	UpdatedAt  time.Time     `json:"updatedAt"`
}

type VClusterSupervisor struct {
	id        string
	host      string
	namespace string
	name      string

	owner *Client

	mu            sync.RWMutex
	state         VClusterState
	stateDetail   string
	generation    uint64
	currentConfig *rest.Config
	currentPort   int
	pfID          string

	subscribers map[uint64]chan VClusterStatus
	subID       uint64
	stopCh      chan struct{}
	stoppedCh   chan struct{}

	startedOnce atomic.Bool
}

func NewVClusterSupervisor(c *Client, id, host, namespace, name string) *VClusterSupervisor {
	return &VClusterSupervisor{
		id:          id,
		host:        host,
		namespace:   namespace,
		name:        name,
		owner:       c,
		state:       VClusterStateConnecting,
		subscribers: make(map[uint64]chan VClusterStatus),
		stopCh:      make(chan struct{}),
		stoppedCh:   make(chan struct{}),
	}
}

func (s *VClusterSupervisor) start() {
	if !s.startedOnce.CompareAndSwap(false, true) {
		return
	}
	go s.run()
}

func (s *VClusterSupervisor) stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	<-s.stoppedCh
}

func (s *VClusterSupervisor) Status() VClusterStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return VClusterStatus{
		ID:         s.id,
		State:      s.state,
		Generation: s.generation,
		LocalPort:  s.currentPort,
		Detail:     s.stateDetail,
		UpdatedAt:  time.Now(),
	}
}

func (s *VClusterSupervisor) Subscribe() (uint64, <-chan VClusterStatus) {
	s.mu.Lock()
	id := s.subID + 1
	s.subID = id
	ch := make(chan VClusterStatus, 8)
	s.subscribers[id] = ch
	current := VClusterStatus{
		ID:         s.id,
		State:      s.state,
		Generation: s.generation,
		LocalPort:  s.currentPort,
		Detail:     s.stateDetail,
		UpdatedAt:  time.Now(),
	}
	s.mu.Unlock()
	select {
	case ch <- current:
	default:
	}
	return id, ch
}

func (s *VClusterSupervisor) Unsubscribe(id uint64) {
	s.mu.Lock()
	if ch, ok := s.subscribers[id]; ok {
		delete(s.subscribers, id)
		close(ch)
	}
	s.mu.Unlock()
}

func (s *VClusterSupervisor) setState(state VClusterState, detail string) {
	s.mu.Lock()
	if s.state == state && s.stateDetail == detail {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.stateDetail = detail
	status := VClusterStatus{
		ID:         s.id,
		State:      s.state,
		Generation: s.generation,
		LocalPort:  s.currentPort,
		Detail:     detail,
		UpdatedAt:  time.Now(),
	}
	subs := make([]chan VClusterStatus, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	log.Printf("[VCLUSTER-SUP] %s state=%s detail=%s gen=%d port=%d", s.id, state, detail, status.Generation, status.LocalPort)
	for _, ch := range subs {
		select {
		case ch <- status:
		default:
		}
	}
}

func (s *VClusterSupervisor) currentRestConfig() *rest.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.currentConfig == nil {
		return nil
	}
	cp := *s.currentConfig
	return &cp
}

func (s *VClusterSupervisor) WaitHealthy(ctx context.Context, timeout time.Duration) (*rest.Config, error) {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.RLock()
		state := s.state
		cfg := s.currentConfig
		detail := s.stateDetail
		s.mu.RUnlock()
		if state == VClusterStateHealthy && cfg != nil {
			cp := *cfg
			return &cp, nil
		}
		if state == VClusterStateFailed && time.Now().After(deadline.Add(-100*time.Millisecond)) {
			return nil, fmt.Errorf("vcluster %s failed: %s", s.id, detail)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("timeout waiting for vcluster %s to be healthy: %s", s.id, detail)
			}
		}
	}
}

func (s *VClusterSupervisor) run() {
	defer close(s.stoppedCh)

	backoff := 500 * time.Millisecond
	const maxBackoff = 15 * time.Second

	for {
		select {
		case <-s.stopCh:
			s.teardownPortForward()
			return
		default:
		}

		if err := s.connectOnce(); err != nil {
			s.setState(VClusterStateReconnecting, err.Error())
			log.Printf("[VCLUSTER-SUP] %s connect failed: %v (retry in %s)", s.id, err, backoff)
			select {
			case <-s.stopCh:
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = 500 * time.Millisecond

		s.runHealthLoop()

		s.teardownPortForward()
		s.setState(VClusterStateReconnecting, "tunnel lost")
	}
}

func (s *VClusterSupervisor) connectOnce() error {
	hostClient, err := s.owner.GetClientForCluster(s.host)
	if err != nil {
		return fmt.Errorf("get host client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	secret, err := hostClient.CoreV1().Secrets(s.namespace).Get(ctx, "vc-"+s.name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("vc-%s secret not found", s.name)
		}
		return fmt.Errorf("read vc secret: %w", err)
	}
	rawKubeconfig, ok := secret.Data["config"]
	if !ok || len(rawKubeconfig) == 0 {
		return fmt.Errorf("vc-%s secret missing config", s.name)
	}

	pod, targetPort, err := resolveVClusterPod(ctx, hostClient, s.namespace, s.name)
	if err != nil {
		return fmt.Errorf("resolve pod: %w", err)
	}

	pf, err := s.owner.portForwardManager.CreatePortForward(s.host, s.namespace, pod, targetPort)
	if err != nil {
		return fmt.Errorf("port-forward: %w", err)
	}

	apiCfg, err := clientcmd.Load(rawKubeconfig)
	if err != nil {
		_ = s.owner.portForwardManager.StopPortForward(pf.ID)
		return fmt.Errorf("parse kubeconfig: %w", err)
	}
	for _, cl := range apiCfg.Clusters {
		cl.Server = fmt.Sprintf("https://localhost:%d", pf.LocalPort)
		cl.InsecureSkipTLSVerify = true
		cl.CertificateAuthority = ""
		cl.CertificateAuthorityData = nil
	}
	restCfg, err := clientcmd.NewDefaultClientConfig(*apiCfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		_ = s.owner.portForwardManager.StopPortForward(pf.ID)
		return fmt.Errorf("build rest config: %w", err)
	}
	restCfg.QPS = 50.0
	restCfg.Burst = 100
	restCfg.ContentType = "application/vnd.kubernetes.protobuf"
	restCfg.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	restCfg.Timeout = 0

	if err := waitForVClusterReady(restCfg, 12*time.Second); err != nil {
		_ = s.owner.portForwardManager.StopPortForward(pf.ID)
		return fmt.Errorf("vcluster not ready: %w", err)
	}

	s.mu.Lock()
	s.generation++
	s.currentConfig = restCfg
	s.currentPort = pf.LocalPort
	s.pfID = pf.ID
	s.mu.Unlock()
	log.Printf("[VCLUSTER-SUP] %s connected gen=%d port=%d pod=%s", s.id, s.generation, pf.LocalPort, pod)
	s.invalidateClientCaches()
	s.setState(VClusterStateHealthy, "")
	return nil
}

func (s *VClusterSupervisor) runHealthLoop() {
	probeInterval := 4 * time.Second
	failureThreshold := 2
	failures := 0
	t := time.NewTicker(probeInterval)
	defer t.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
		}

		cfg := s.currentRestConfig()
		if cfg == nil {
			return
		}
		ok := s.healthProbe(cfg)
		if ok {
			if failures > 0 {
				log.Printf("[VCLUSTER-SUP] %s recovered after %d failed probes", s.id, failures)
			}
			failures = 0
			continue
		}
		failures++
		log.Printf("[VCLUSTER-SUP] %s probe failed (%d/%d)", s.id, failures, failureThreshold)
		s.setState(VClusterStateReconnecting, "health probe failing")
		if failures >= failureThreshold {
			return
		}
	}
}

func (s *VClusterSupervisor) healthProbe(cfg *rest.Config) bool {
	probeCfg := *cfg
	probeCfg.Timeout = 3 * time.Second
	dc, err := discovery.NewDiscoveryClientForConfig(&probeCfg)
	if err != nil {
		return false
	}
	if _, err := dc.ServerVersion(); err != nil {
		return false
	}
	if pf, ok := s.owner.portForwardManager.GetPortForward(s.pfID); !ok || !pf.Active {
		return false
	}
	return true
}

func (s *VClusterSupervisor) teardownPortForward() {
	s.mu.Lock()
	pfID := s.pfID
	s.pfID = ""
	s.currentConfig = nil
	s.currentPort = 0
	s.mu.Unlock()
	if pfID != "" {
		_ = s.owner.portForwardManager.StopPortForward(pfID)
	}
	s.invalidateClientCaches()
}

func (s *VClusterSupervisor) invalidateClientCaches() {
	s.owner.mu.Lock()
	delete(s.owner.configs, s.id)
	delete(s.owner.clients, s.id)
	delete(s.owner.dynamic, s.id)
	delete(s.owner.interactive, s.id)
	delete(s.owner.metadata, s.id)
	delete(s.owner.bulkMetadata, s.id)
	delete(s.owner.discovery, s.id)
	s.owner.mu.Unlock()

	s.owner.resourceNameMu.Lock()
	prefix := s.id + ":"
	for k := range s.owner.resourceNameCache {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(s.owner.resourceNameCache, k)
		}
	}
	s.owner.resourceNameMu.Unlock()
}
