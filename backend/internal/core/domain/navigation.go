package domain

import (
	"time"
)

// NavigationID represents a unique navigation entry identifier
type NavigationID string

// NavigationEntry represents a single navigation action
type NavigationEntry struct {
	ID         NavigationID
	TabID      string
	Timestamp  time.Time
	Type       NavigationType
	Path       string
	ResourceID *ResourceID // Optional, for resource-specific navigation
	ClusterID  ClusterID
	Details    NavigationDetails
	Duration   *time.Duration // Time spent on this view
}

// NavigationType represents the type of navigation action
type NavigationType string

const (
	NavigationTypeResourceList   NavigationType = "resource_list"
	NavigationTypeResourceDetail NavigationType = "resource_detail"
	NavigationTypeSearch         NavigationType = "search"
	NavigationTypeExec           NavigationType = "exec"
	NavigationTypeLogs           NavigationType = "logs"
	NavigationTypeClusterSwitch  NavigationType = "cluster_switch"
	NavigationTypeDashboard      NavigationType = "dashboard"
)

// NavigationDetails contains type-specific navigation details
type NavigationDetails struct {
	// For resource list
	ResourceKind string
	Namespace    string
	Filters      map[string]string

	// For search
	SearchQuery   string
	SearchResults int

	// For exec/logs
	PodName       string
	ContainerName string

	// For cluster switch
	FromCluster string
	ToCluster   string

	// Generic metadata
	Metadata map[string]interface{}
}

// NavigationHistory represents navigation history for a tab
type NavigationHistory struct {
	TabID        string
	ClusterID    ClusterID
	Entries      []NavigationEntry
	CurrentIndex int
	MaxSize      int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NavigationBreadcrumb represents a breadcrumb in navigation
type NavigationBreadcrumb struct {
	Label      string
	Path       string
	ResourceID *ResourceID
	Type       NavigationType
	Active     bool
}

// TabHistory represents the navigation history for a single tab
type TabHistory struct {
	ClusterID string            `json:"cluster_id"`
	Label     string            `json:"label"`
	Entries   []NavigationEntry `json:"entries"`
	Index     int               `json:"index"`
}
