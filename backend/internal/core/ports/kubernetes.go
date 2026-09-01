package ports

import (
	"context"
	"io"

	"github.com/kanivet/backend/internal/core/domain"
	"k8s.io/client-go/tools/remotecommand"
)

// KubernetesClient defines the interface for Kubernetes operations
type KubernetesClient interface {
	// Resource operations
	GetResource(ctx context.Context, id domain.ResourceID) (*domain.Resource, error)
	ListResources(ctx context.Context, query domain.ResourceQuery) (*domain.ResourceList, error)
	CreateResource(ctx context.Context, resource domain.Resource) (*domain.Resource, error)
	UpdateResource(ctx context.Context, resource domain.Resource) (*domain.Resource, error)
	DeleteResource(ctx context.Context, id domain.ResourceID) error
	PatchResource(ctx context.Context, id domain.ResourceID, patch []byte, patchType string) (*domain.Resource, error)

	// Discovery operations
	GetResourceTypes(ctx context.Context, cluster string) ([]domain.ResourceMetadata, error)
	GetAPIResources(ctx context.Context, cluster string) ([]APIResource, error)
	GetServerVersion(ctx context.Context, cluster string) (string, error)

	// Watch operations
	WatchResource(ctx context.Context, query domain.ResourceQuery) (ResourceWatcher, error)

	// Execution operations
	ExecuteInPod(ctx context.Context, exec PodExecRequest) error
	ExecuteInNode(ctx context.Context, exec NodeExecRequest) error
	StreamPodLogs(ctx context.Context, req PodLogRequest) (io.ReadCloser, error)

	// Port forwarding
	PortForward(ctx context.Context, req PortForwardRequest) (PortForwarder, error)

	// Cluster operations
	GetClusterInfo(ctx context.Context, cluster string) (*ClusterInfo, error)
	ValidateClusterAccess(ctx context.Context, cluster string) error
}

// ResourceWatcher represents a watch operation on resources
type ResourceWatcher interface {
	// Events returns the channel to receive resource events
	Events() <-chan domain.ResourceEvent

	// Stop stops the watcher
	Stop()

	// Error returns any error that occurred during watching
	Error() error
}

// PodExecRequest represents a request to execute a command in a pod
type PodExecRequest struct {
	ClusterID    string
	Namespace    string
	PodName      string
	Container    string
	Command      []string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	TTY          bool
	TerminalSize *remotecommand.TerminalSize
}

// NodeExecRequest represents a request to execute a command on a node
type NodeExecRequest struct {
	ClusterID    string
	NodeName     string
	Command      []string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	TTY          bool
	TerminalSize *remotecommand.TerminalSize
}

// PodLogRequest represents a request to stream pod logs
type PodLogRequest struct {
	ClusterID                    string
	Namespace                    string
	PodName                      string
	Container                    string
	Follow                       bool
	Previous                     bool
	SinceSeconds                 *int64
	SinceTime                    *string
	Timestamps                   bool
	TailLines                    *int64
	LimitBytes                   *int64
	InsecureSkipTLSVerifyBackend bool
}

// PortForwardRequest represents a port forwarding request
type PortForwardRequest struct {
	ClusterID  string
	Namespace  string
	PodName    string
	LocalPort  int
	RemotePort int
	Addresses  []string
}

// PortForwarder represents an active port forwarding session
type PortForwarder interface {
	// Ready returns a channel that is closed when port forwarding is ready
	Ready() <-chan struct{}

	// Stop stops the port forwarding
	Stop()

	// Error returns any error that occurred
	Error() error

	// GetPorts returns the local and remote ports
	GetPorts() (local int, remote int)
}

// APIResource represents a Kubernetes API resource
type APIResource struct {
	Name         string
	SingularName string
	Namespaced   bool
	Group        string
	Version      string
	Kind         string
	Verbs        []string
	ShortNames   []string
	Categories   []string
}

// ClusterInfo contains information about a Kubernetes cluster
type ClusterInfo struct {
	Version        string
	Platform       string
	GitVersion     string
	GitCommit      string
	BuildDate      string
	NodeCount      int
	PodCount       int
	NamespaceCount int
}

// KubernetesError represents a Kubernetes-specific error
type KubernetesError struct {
	StatusCode int
	Reason     string
	Message    string
	Details    map[string]interface{}
}

func (e KubernetesError) Error() string {
	return e.Message
}

// IsNotFound returns true if the error is a not found error
func IsNotFound(err error) bool {
	if kErr, ok := err.(KubernetesError); ok {
		return kErr.StatusCode == 404
	}
	return false
}

// IsUnauthorized returns true if the error is an unauthorized error
func IsUnauthorized(err error) bool {
	if kErr, ok := err.(KubernetesError); ok {
		return kErr.StatusCode == 401 || kErr.StatusCode == 403
	}
	return false
}

// IsConflict returns true if the error is a conflict error
func IsConflict(err error) bool {
	if kErr, ok := err.(KubernetesError); ok {
		return kErr.StatusCode == 409
	}
	return false
}
