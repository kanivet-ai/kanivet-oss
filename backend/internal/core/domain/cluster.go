package domain

import (
	"time"
)

// ClusterID represents a unique cluster identifier
type ClusterID string

// Cluster represents a Kubernetes cluster
type Cluster struct {
	ID          ClusterID
	Name        string
	Context     string
	Server      string
	Namespace   string // Default namespace
	Group       string // Cluster group (dev, qa, prod, etc.)
	Description string
	Tags        []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastPingAt  *time.Time
	Status      ClusterStatus
}

// ClusterStatus represents the current status of a cluster
type ClusterStatus struct {
	State        ClusterState
	Message      string
	Version      string // Kubernetes version
	NodeCount    int
	LastChecked  time.Time
	IsReachable  bool
	ResponseTime time.Duration
}

// ClusterState represents the state of a cluster
type ClusterState string

const (
	ClusterStateUnknown     ClusterState = "unknown"
	ClusterStateHealthy     ClusterState = "healthy"
	ClusterStateDegraded    ClusterState = "degraded"
	ClusterStateUnreachable ClusterState = "unreachable"
	ClusterStateMaintenance ClusterState = "maintenance"
)

// ClusterGroup represents a logical grouping of clusters
type ClusterGroup struct {
	ID           string
	Name         string
	Description  string
	Clusters     []ClusterID
	IsPredefined bool
	IsDeleted    bool
	Order        int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ClusterAssignment represents the assignment of a cluster to a group
type ClusterAssignment struct {
	ClusterID ClusterID
	GroupID   string
	CreatedAt time.Time
}

// PredefinedGroups defines the built-in cluster groups
var PredefinedGroups = []ClusterGroup{
	{
		Name:         "dev",
		Description:  "Development environments",
		IsPredefined: true,
		Order:        1,
	},
	{
		Name:         "qa",
		Description:  "Quality Assurance environments",
		IsPredefined: true,
		Order:        2,
	},
	{
		Name:         "stg",
		Description:  "Staging environments",
		IsPredefined: true,
		Order:        3,
	},
	{
		Name:         "prod",
		Description:  "Production environments",
		IsPredefined: true,
		Order:        4,
	},
	{
		Name:         "lab",
		Description:  "Lab/Experimental environments",
		IsPredefined: true,
		Order:        5,
	},
}

// ClusterQuery represents query parameters for listing clusters
type ClusterQuery struct {
	Groups    []string
	States    []ClusterState
	Tags      []string
	Search    string
	Limit     int
	Offset    int
	SortBy    string
	SortOrder string
}

// ClusterList represents a list of clusters with metadata
type ClusterList struct {
	Items  []Cluster
	Total  int
	Limit  int
	Offset int
}

// ClusterEvent represents an event related to a cluster
type ClusterEvent struct {
	Type      ClusterEventType
	ClusterID ClusterID
	Timestamp time.Time
	Details   map[string]interface{}
}

// ClusterEventType represents the type of cluster event
type ClusterEventType string

const (
	ClusterEventTypeAdded       ClusterEventType = "CLUSTER_ADDED"
	ClusterEventTypeUpdated     ClusterEventType = "CLUSTER_UPDATED"
	ClusterEventTypeDeleted     ClusterEventType = "CLUSTER_DELETED"
	ClusterEventTypeHealthCheck ClusterEventType = "CLUSTER_HEALTH_CHECK"
)
