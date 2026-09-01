package k8s

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
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
	stopChan   chan struct{}
	readyChan  chan struct{}
}

type PortForwardManager struct {
	mu       sync.RWMutex
	forwards map[string]*PortForward
	client   *Client
}

func NewPortForwardManager(client *Client) *PortForwardManager {
	return &PortForwardManager{
		forwards: make(map[string]*PortForward),
		client:   client,
	}
}

func (m *PortForwardManager) CreatePortForward(cluster, namespace, podName string, remotePort int) (*PortForward, error) {
	log.Printf("Creating port forward for %s/%s:%d in cluster %s", namespace, podName, remotePort, cluster)

	// Find an available local port
	localPort, err := m.findAvailablePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find available port: %w", err)
	}
	log.Printf("Found available local port: %d", localPort)

	id := fmt.Sprintf("%s-%s-%s-%d", cluster, namespace, podName, remotePort)

	m.mu.Lock()
	// Check if already exists
	if existing, ok := m.forwards[id]; ok && existing.Active {
		m.mu.Unlock()
		log.Printf("Port forward already exists for %s", id)
		return existing, nil
	}

	pf := &PortForward{
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
	m.mu.Unlock()

	// Start the port forward in a goroutine
	go func() {
		if err := m.startPortForward(pf); err != nil {
			log.Printf("Port forward %s failed: %v", id, err)
			m.mu.Lock()
			delete(m.forwards, id)
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
		delete(m.forwards, id)
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
			delete(m.forwards, pf.ID)
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

func (m *PortForwardManager) StopPortForward(id string) error {
	log.Printf("Stopping port forward: %s", id)

	m.mu.Lock()
	defer m.mu.Unlock()

	pf, ok := m.forwards[id]
	if !ok {
		log.Printf("Port forward not found (already stopped?): %s", id)
		// Don't return an error if the port forward doesn't exist - it may have already been cleaned up
		return nil
	}

	if pf.Active && pf.stopChan != nil {
		select {
		case pf.stopChan <- struct{}{}:
			log.Printf("Successfully sent stop signal for %s", id)
		default:
			log.Printf("Stop channel might be closed for %s", id)
		}
		pf.Active = false
	}

	delete(m.forwards, id)
	log.Printf("Port forward %s removed from active forwards", id)
	return nil
}

func (m *PortForwardManager) GetPortForward(id string) (*PortForward, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pf, ok := m.forwards[id]
	return pf, ok
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
