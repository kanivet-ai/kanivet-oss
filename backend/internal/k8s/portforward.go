package k8s

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type PortForward struct {
	ID         string
	Cluster    string
	Namespace  string
	PodName    string
	LocalPort  int
	RemotePort int
	Active     bool
	CreatedAt  time.Time
	// SourceName and SourceKind are what the user forwarded from (pod or service).
	SourceName string
	SourceKind string
	// SourcePort is the service port when the forward was created from a Service.
	SourcePort int
	stopChan   chan struct{}
	readyChan  chan struct{}
}

type PortForwardManager struct {
	mu       sync.RWMutex
	forwards map[string]*PortForward
	client   *Client
	// seq numbers private forwards so their IDs are never shared.
	seq atomic.Uint64
	// start runs a forward until it is stopped; replaced in tests.
	start func(pf *PortForward) error
}

func NewPortForwardManager(client *Client) *PortForwardManager {
	m := &PortForwardManager{
		forwards: make(map[string]*PortForward),
		client:   client,
	}
	m.start = m.startPortForward
	return m
}

// CreatePortForward opens a forward to pod:port, or hands back the one
// already open to the same target. Stopping it stops it for everyone holding
// it, which is what the user's own port-forwards want.
func (m *PortForwardManager) CreatePortForward(cluster, namespace, podName string, remotePort, preferredLocalPort int) (*PortForward, error) {
	return m.create(cluster, namespace, podName, remotePort, true, preferredLocalPort)
}

// CreatePrivatePortForward always opens a new forward under an ID nobody else
// gets, so its owner can stop it without tearing down a tunnel the user (or
// another of Kanivet's own users) is still reading through, and nobody else's
// Stop can tear down the owner's.
func (m *PortForwardManager) CreatePrivatePortForward(cluster, namespace, podName string, remotePort int) (*PortForward, error) {
	return m.create(cluster, namespace, podName, remotePort, false, 0)
}

func (m *PortForwardManager) create(cluster, namespace, podName string, remotePort int, shared bool, preferredLocalPort int) (*PortForward, error) {
	log.Printf("Creating port forward for %s/%s:%d in cluster %s", namespace, podName, remotePort, cluster)

	localPort, err := m.allocateLocalPort(preferredLocalPort)
	if err != nil {
		return nil, err
	}
	log.Printf("Found available local port: %d", localPort)

	id := fmt.Sprintf("%s-%s-%s-%d", cluster, namespace, podName, remotePort)
	if !shared {
		id += "#" + strconv.FormatUint(m.seq.Add(1), 10)
	}

	m.mu.Lock()
	if existing, ok := m.forwards[id]; ok && existing.Active {
		m.mu.Unlock()
		log.Printf("Port forward already exists for %s", id)
		return existing, nil
	}

	var pf *PortForward
	if existing, ok := m.forwards[id]; ok && !existing.Active {
		existing.LocalPort = localPort
		existing.Active = false
		existing.stopChan = make(chan struct{}, 1)
		existing.readyChan = make(chan struct{})
		pf = existing
	} else {
		pf = &PortForward{
			ID:         id,
			Cluster:    cluster,
			Namespace:  namespace,
			PodName:    podName,
			LocalPort:  localPort,
			RemotePort: remotePort,
			Active:     false,
			CreatedAt:  time.Now(),
			stopChan:   make(chan struct{}, 1),
			readyChan:  make(chan struct{}),
		}
		m.forwards[id] = pf
	}
	m.mu.Unlock()

	// Start the port forward in a goroutine
	go func() {
		if err := m.start(pf); err != nil {
			log.Printf("Port forward %s failed: %v", id, err)
			m.mu.Lock()
			if strings.Contains(id, "#") {
				delete(m.forwards, id)
			} else if existing, ok := m.forwards[id]; ok {
				existing.Active = false
			}
			m.mu.Unlock()
		}
	}()

	// Wait for ready or timeout
	select {
	case <-pf.readyChan:
		m.mu.Lock()
		pf.Active = true
		m.mu.Unlock()
		log.Printf("Port forward %s is now active", id)
		return pf, nil
	case <-time.After(10 * time.Second):
		m.mu.Lock()
		// Check if it was deleted due to error
		if _, exists := m.forwards[id]; !exists {
			m.mu.Unlock()
			return nil, fmt.Errorf("port forward failed to start")
		}
		if strings.Contains(id, "#") {
			delete(m.forwards, id)
		} else if existing, ok := m.forwards[id]; ok {
			existing.Active = false
		}
		m.mu.Unlock()
		// Try to stop the goroutine
		select {
		case pf.stopChan <- struct{}{}:
		default:
		}
		log.Printf("Timeout waiting for port forward %s to be ready", id)
		return nil, fmt.Errorf("timeout waiting for port forward to be ready")
	}
}

func (m *PortForwardManager) startPortForward(pf *PortForward) error {
	log.Printf("Starting port forward for %s", pf.ID)

	config, err := m.client.getConfigForCluster(pf.Cluster)
	if err != nil {
		return fmt.Errorf("failed to get cluster config: %w", err)
	}

	client, err := m.client.GetClientForCluster(pf.Cluster)
	if err != nil {
		return fmt.Errorf("failed to get k8s client: %w", err)
	}

	// Verify the pod exists
	pod, err := client.CoreV1().Pods(pf.Namespace).Get(context.Background(), pf.PodName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}
	log.Printf("Found pod %s in namespace %s, status: %s", pf.PodName, pf.Namespace, pod.Status.Phase)

	// Create the URL for the pod's portforward endpoint
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(pf.Namespace).
		Name(pf.PodName).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())

	// Create streams
	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{})

	out := io.Discard
	errOut := io.Discard

	ports := []string{fmt.Sprintf("%d:%d", pf.LocalPort, pf.RemotePort)}

	fw, err := portforward.New(dialer, ports, stopChan, readyChan, out, errOut)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	log.Printf("Starting port forwarding from localhost:%d to pod:%d", pf.LocalPort, pf.RemotePort)

	// Start forwarding in a separate goroutine
	go func() {
		err := fw.ForwardPorts()
		if err != nil {
			log.Printf("Port forward error for %s: %v", pf.ID, err)
		}
		// Clean up when ForwardPorts exits
		m.mu.Lock()
		if storedPF, ok := m.forwards[pf.ID]; ok && storedPF == pf {
			storedPF.Active = false
			if strings.Contains(pf.ID, "#") {
				delete(m.forwards, pf.ID)
			}
			log.Printf("Port forward %s cleaned up after ForwardPorts exit", pf.ID)
		}
		m.mu.Unlock()
		// Unblock outer goroutine parked on pf.stopChan so it doesn't leak
		// when the forward dies on its own (pod restart, network drop).
		select {
		case pf.stopChan <- struct{}{}:
		default:
		}
	}()

	// Wait for ready signal
	select {
	case <-readyChan:
		log.Printf("Port forward ready for %s", pf.ID)
		close(pf.readyChan)
		// Port forward is ready, now wait for stop signal
		<-pf.stopChan
		log.Printf("Stopping port forward for %s", pf.ID)
		close(stopChan)
	case <-time.After(30 * time.Second):
		log.Printf("Timeout waiting for port forward to be ready for %s", pf.ID)
		close(stopChan)
		return fmt.Errorf("timeout waiting for port forward to be ready")
	}

	return nil
}

func (m *PortForwardManager) stopTunnel(pf *PortForward, id string) {
	if pf.Active && pf.stopChan != nil {
		select {
		case pf.stopChan <- struct{}{}:
			log.Printf("Successfully sent stop signal for %s", id)
		default:
			log.Printf("Stop channel might be closed for %s", id)
		}
	}
	pf.Active = false
}

func (m *PortForwardManager) StopPortForward(id string) error {
	log.Printf("Stopping port forward: %s", id)

	m.mu.Lock()
	defer m.mu.Unlock()

	pf, ok := m.forwards[id]
	if !ok {
		log.Printf("Port forward not found (already stopped?): %s", id)
		return nil
	}

	m.stopTunnel(pf, id)
	if strings.Contains(id, "#") {
		delete(m.forwards, id)
		log.Printf("Port forward %s removed (private)", id)
		return nil
	}
	log.Printf("Port forward %s stopped (record kept)", id)
	return nil
}

func (m *PortForwardManager) RemovePortForward(id string) error {
	log.Printf("Removing port forward record: %s", id)

	m.mu.Lock()
	defer m.mu.Unlock()

	pf, ok := m.forwards[id]
	if !ok {
		return nil
	}
	m.stopTunnel(pf, id)
	delete(m.forwards, id)
	return nil
}

func (m *PortForwardManager) findForward(id string) (*PortForward, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if pf, ok := m.forwards[id]; ok {
		return pf, true
	}
	if strings.Contains(id, "%") {
		decoded, err := url.PathUnescape(id)
		if err == nil {
			if pf, ok := m.forwards[decoded]; ok {
				return pf, true
			}
		}
	}
	return nil, false
}

// ResumePortForward starts a stopped user forward again, keeping its metadata and local port.
func (m *PortForwardManager) ResumePortForward(id string) (*PortForward, error) {
	pf, ok := m.findForward(id)
	if !ok {
		return nil, fmt.Errorf("port forward not found: %s", id)
	}
	if pf.Active {
		return pf, nil
	}
	if strings.Contains(id, "#") {
		return nil, fmt.Errorf("cannot resume private forward %s", id)
	}
	return m.create(pf.Cluster, pf.Namespace, pf.PodName, pf.RemotePort, true, pf.LocalPort)
}

func (m *PortForwardManager) GetPortForward(id string) (*PortForward, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pf, ok := m.forwards[id]
	return pf, ok
}

// ListPortForwards returns user-visible forwards (shared), active or stopped.
// Private forwards Kanivet opens for metrics and probes use IDs with a "#" suffix.
func (m *PortForwardManager) ListPortForwards(cluster string) []*PortForward {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*PortForward, 0, len(m.forwards))
	for _, pf := range m.forwards {
		if strings.Contains(pf.ID, "#") {
			continue
		}
		if cluster != "" && pf.Cluster != cluster {
			continue
		}
		out = append(out, pf)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (m *PortForwardManager) allocateLocalPort(preferred int) (int, error) {
	if preferred > 0 {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
		if err != nil {
			return 0, fmt.Errorf("local port %d is not available: %w", preferred, err)
		}
		_ = listener.Close()
		return preferred, nil
	}
	return m.findAvailablePort()
}

func (m *PortForwardManager) findAvailablePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() {
		// Ignore close error in defer - port is already allocated successfully
		_ = listener.Close()
	}()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}
