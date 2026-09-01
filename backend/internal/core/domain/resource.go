package domain

import (
	"time"
)

// ResourceID represents a unique identifier for a Kubernetes resource
type ResourceID struct {
	Cluster   string
	Namespace string
	Group     string
	Version   string
	Kind      string
	Name      string
}

// Resource represents a Kubernetes resource with its metadata
type Resource struct {
	ID              ResourceID
	APIVersion      string
	Labels          map[string]string
	Annotations     map[string]string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletionTime    *time.Time
	OwnerReferences []OwnerReference
	Status          ResourceStatus
	Spec            map[string]interface{} // Generic spec storage
	Data            map[string]interface{} // Full resource data
}

// OwnerReference represents a resource's owner
type OwnerReference struct {
	APIVersion string
	Kind       string
	Name       string
	UID        string
	Controller *bool
}

// ResourceStatus represents the status of a resource
type ResourceStatus struct {
	Phase      string
	Conditions []Condition
	Ready      bool
	Message    string
	Reason     string
}

// Condition represents a resource condition
type Condition struct {
	Type               string
	Status             string
	LastTransitionTime time.Time
	Reason             string
	Message            string
}

// ResourceList represents a list of resources with metadata
type ResourceList struct {
	Items      []Resource
	Total      int
	Limit      int
	Offset     int
	APIVersion string
	Kind       string
	Continue   string // For pagination
}

// ResourceQuery represents query parameters for listing resources
type ResourceQuery struct {
	Cluster       string
	Namespace     string
	Group         string
	Version       string
	Kind          string
	LabelSelector map[string]string
	FieldSelector map[string]string
	Limit         int
	Offset        int
	Continue      string
}

// ResourceMetadata contains metadata about a resource type
type ResourceMetadata struct {
	Group      string
	Version    string
	Kind       string
	Name       string // Plural name
	Namespaced bool
	Verbs      []string
	Categories []string
	ShortNames []string
}

// ResourceEvent represents an event related to a resource
type ResourceEvent struct {
	Type      EventType
	Resource  Resource
	Timestamp time.Time
}

// EventType represents the type of resource event
type EventType string

const (
	EventTypeAdded    EventType = "ADDED"
	EventTypeModified EventType = "MODIFIED"
	EventTypeDeleted  EventType = "DELETED"
	EventTypeError    EventType = "ERROR"
)
