# Search Module

The search module provides fast indexing and searching capabilities for Kubernetes resources.

## Architecture

Simple, focused architecture:

- `storage/` - Storage implementations (memory index, background indexing, types)
- `service.go` - Main service implementation with embedded configuration
- `types.go` - API type re-exports for backward compatibility

No unnecessary abstractions or interfaces - just the essential components.

## Features

- **Fast In-Memory Search**: Uses an inverted index for quick full-text search
- **Background Indexing**: Indexes clusters incrementally without blocking
- **Real-time Updates**: Watches for resource changes and updates index
- **Search History**: Tracks recent searches for suggestions
- **Fuzzy Matching**: Supports approximate string matching
- **Multi-Cluster**: Indexes and searches across multiple clusters

## Usage

```go
import "github.com/kanivet/backend/internal/search"

// Create service
service := search.NewService(k8sClient, cache)

// Index a cluster
err := service.IndexCluster("production")

// Search
results, err := service.Search("nginx", search.SearchOptions{
    Limit: 20,
    Clusters: []string{"production"},
})

// Get suggestions
suggestions, err := service.GetSuggestions("dep", 5)
```

## Implementation Details

The module maintains full backward compatibility with the existing API. All functionality from the original search module is preserved, but the internal architecture is simplified and decoupled from unnecessary abstractions.

### Architectural Improvements Made

1. **Removed Core Abstraction Layer**: Eliminated unnecessary `core/` directory with unused interfaces
2. **Simplified Structure**: Now follows a pattern similar to the websocket module but appropriate for search functionality
3. **Removed Duplicate Watcher Logic**: The search module no longer creates its own resource watchers

### Future Architectural Improvements Needed

1. **Integrate with Centralized Watcher Service**: The search module should subscribe to events from `internal/k8s/watcher` instead of managing its own watchers
2. **Event-Driven Index Updates**: Instead of initial indexing only, the search index should be updated in real-time via watcher events
3. **Unified Resource Watching**: All resource watching should go through the centralized k8s watcher service to avoid duplicate watches

This would eliminate resource watching duplication across the codebase and ensure consistent resource change handling.
