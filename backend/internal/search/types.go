package search

import (
	"time"

	"github.com/kanivet/backend/internal/search/storage"
)

// Re-export storage types for API compatibility
type SearchableResource = storage.SearchableResource
type SearchResult = storage.SearchResult
type SearchMatch = storage.SearchMatch
type SearchQuery = storage.SearchQuery
type IndexStats = storage.IndexStats

// IndexerStatus represents the current state of search indexing
type IndexerStatus struct {
	Active           bool                      `json:"active"`
	StartTime        time.Time                 `json:"startTime,omitempty"`
	LastUpdateTime   time.Time                 `json:"lastUpdateTime,omitempty"`
	ResourcesTotal   int                       `json:"resourcesTotal"`
	ResourcesIndexed int                       `json:"resourcesIndexed"`
	CurrentStatus    string                    `json:"currentStatus"`
	Progress         int                       `json:"progress"`
	Errors           []string                  `json:"errors,omitempty"`
	ResourceStatuses map[string]ResourceStatus `json:"resourceStatuses,omitempty"`
}

// ResourceStatus represents the indexing status for a specific resource type
type ResourceStatus struct {
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Indexed   int    `json:"indexed"`
	Completed bool   `json:"completed"`
}

// SearchOptions provides search configuration
type SearchOptions struct {
	Clusters   []string `json:"clusters"`
	Namespaces []string `json:"namespaces"`
	Kinds      []string `json:"kinds"`
	Limit      int      `json:"limit"`
	Offset     int      `json:"offset"`
}

// Re-export SearchCategories
var SearchCategories = storage.SearchCategories
