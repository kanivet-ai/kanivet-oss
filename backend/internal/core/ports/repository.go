package ports

import (
	"context"

	"github.com/kanivet/backend/internal/core/domain"
)

// ResourceRepository defines the interface for resource data access
type ResourceRepository interface {
	// List queries resources based on the provided query parameters
	List(ctx context.Context, query domain.ResourceQuery) (*domain.ResourceList, error)

	// Get retrieves a specific resource by its ID
	Get(ctx context.Context, id domain.ResourceID) (*domain.Resource, error)

	// GetMultiple retrieves multiple resources by their IDs
	GetMultiple(ctx context.Context, ids []domain.ResourceID) ([]domain.Resource, error)

	// Count returns the count of resources matching the query
	Count(ctx context.Context, query domain.ResourceQuery) (int, error)

	// GetResourceTypes returns available resource types for a cluster
	GetResourceTypes(ctx context.Context, cluster string) ([]domain.ResourceMetadata, error)

	// Watch watches for resource changes and sends events to the channel
	Watch(ctx context.Context, query domain.ResourceQuery) (<-chan domain.ResourceEvent, error)
}

// ClusterRepository defines the interface for cluster data access
type ClusterRepository interface {
	// List returns all clusters based on query parameters
	List(ctx context.Context, query domain.ClusterQuery) (*domain.ClusterList, error)

	// Get retrieves a specific cluster by its ID
	Get(ctx context.Context, id domain.ClusterID) (*domain.Cluster, error)

	// GetByContext retrieves a cluster by its context name
	GetByContext(ctx context.Context, contextName string) (*domain.Cluster, error)

	// Save creates or updates a cluster
	Save(ctx context.Context, cluster *domain.Cluster) error

	// Delete removes a cluster
	Delete(ctx context.Context, id domain.ClusterID) error

	// UpdateStatus updates the cluster status
	UpdateStatus(ctx context.Context, id domain.ClusterID, status domain.ClusterStatus) error

	// ListGroups returns all cluster groups
	ListGroups(ctx context.Context) ([]domain.ClusterGroup, error)

	// GetGroup retrieves a specific cluster group
	GetGroup(ctx context.Context, groupID string) (*domain.ClusterGroup, error)

	// SaveGroup creates or updates a cluster group
	SaveGroup(ctx context.Context, group *domain.ClusterGroup) error

	// DeleteGroup removes a cluster group
	DeleteGroup(ctx context.Context, groupID string) error

	// AssignToGroup assigns a cluster to a group
	AssignToGroup(ctx context.Context, clusterID domain.ClusterID, groupID string) error

	// UnassignFromGroup removes a cluster from a group
	UnassignFromGroup(ctx context.Context, clusterID domain.ClusterID, groupID string) error
}

// Transaction represents a database transaction
type Transaction interface {
	Commit() error
	Rollback() error
}

// TransactionalRepository provides transaction support
type TransactionalRepository interface {
	// BeginTransaction starts a new transaction
	BeginTransaction(ctx context.Context) (Transaction, error)

	// WithTransaction executes a function within a transaction
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// RepositoryFactory creates repository instances
type RepositoryFactory interface {
	// CreateResourceRepository creates a resource repository
	CreateResourceRepository() ResourceRepository

	// CreateClusterRepository creates a cluster repository
	CreateClusterRepository() ClusterRepository
}
