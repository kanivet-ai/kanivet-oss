# Unified Resource Management Architecture

## Core Principle: Use Existing Primitives

We have well-designed infrastructure primitives that should be used by ALL services:

### 1. **Cache Service** (`/internal/cache`)
- Provides TTL-based caching with `GetOrSet` pattern
- Handles concurrent access
- Automatic expiration
- **MUST USE**: All data caching should go through this service

### 2. **K8s Watcher Service** (`/internal/k8s/watcher`)
- Reference-counted watching (multiple consumers, one watch)
- Automatic reconnection
- Topic-based broadcasting
- **MUST USE**: Never create direct k8s watches

### 3. **WebSocket Hub** (`/internal/websocket/core`)
- Connection management
- Message broadcasting
- Topic subscriptions
- **MUST USE**: All real-time updates go through this

## Architecture Layers

### Layer 1: Infrastructure Primitives (Already Built)
```
┌─────────────┐  ┌──────────────┐  ┌─────────────┐
│Cache Service│  │Watcher Service│  │WebSocket Hub│
└─────────────┘  └──────────────┘  └─────────────┘
```

### Layer 2: Unified Resource Cache (New)
```go
// Single source of truth for ALL Kubernetes resources
type UnifiedResourceCache struct {
    watcher *watcher.Service  // Uses existing watcher
    cache   *cache.Cache      // Uses existing cache

    // NO custom watching logic
    // NO custom caching logic
}
```

### Layer 3: Domain Services
```go
// Pod Status Service - thin wrapper around UnifiedResourceCache
type PodStatusService struct {
    resourceCache *UnifiedResourceCache  // Delegates to unified cache
    // NO direct k8s access
    // NO custom caching
}

// Search Service - indexes data from UnifiedResourceCache
type SearchService struct {
    resourceCache *UnifiedResourceCache  // Gets data from cache
    index         *SearchIndex           // Only maintains search index
    // NO k8s watching
}
```

## Key Design Rules

### 1. Single Watcher per Resource Type per Namespace
```go
// BAD: Multiple watchers for same resources
watcher.StartPodStatusWatch(cluster, ns, selector1, uid1)  // Watcher 1
watcher.StartPodStatusWatch(cluster, ns, selector2, uid2)  // Watcher 2

// GOOD: One watcher for all pods in namespace
watcher.StartWatch(cluster, "", "v1", "pods", ns)  // One watcher
```

### 2. Use Central Cache for Everything
```go
// BAD: Custom caching
type Service struct {
    cache map[string]interface{}  // Custom cache
}

// GOOD: Use central cache service
type Service struct {
    cache *cache.Cache  // Reuse existing
}
```

### 3. Bridge Pattern for Integration
```go
// Bridge converts k8s watcher events to domain events
type ResourceBridge struct {
    resourceCache *UnifiedResourceCache
}

func (b *ResourceBridge) Broadcast(topic string, msg watcher.Message) error {
    // Parse k8s event
    // Update unified cache
    // Notify domain services
}
```

## Implementation Plan

### Phase 1: Unified Resource Cache
1. Create `UnifiedResourceCache` that uses existing watcher & cache
2. Implement efficient namespace-level watching
3. Add label indexing for fast queries

### Phase 2: Migrate Services
1. Update `PodStatusService` to use `UnifiedResourceCache`
2. Update `SearchService` to use `UnifiedResourceCache`
3. Remove all direct k8s watches

### Phase 3: Optimize
1. Add request coalescing
2. Implement intelligent pre-fetching
3. Add metrics and monitoring

## Benefits

1. **Performance**: One watch per namespace instead of hundreds
2. **Consistency**: Single source of truth
3. **Reusability**: All services share same data
4. **Maintainability**: Clear separation of concerns
5. **Scalability**: Handles thousands of resources efficiently

## Example: Pod Status Flow

### Current (Inefficient)
```
1. HTTP Request → PodStatusService
2. PodStatusService → Start new k8s watch for each resource
3. K8s API → Throttling (too many watches)
4. Response → 3+ seconds
```

### New (Efficient)
```
1. HTTP Request → PodStatusService
2. PodStatusService → UnifiedResourceCache.GetPodStatuses()
3. UnifiedResourceCache → Query in-memory cache (instant)
4. Response → <10ms
```

## Code Example

```go
// Initialize once in main.go
unifiedCache := services.NewUnifiedResourceCache(watcherService, cacheService)

// Pod Status Service just queries the cache
podStatusService := &PodStatusService{
    cache: unifiedCache,
}

// Search Service listens for changes
searchService := &SearchService{
    cache: unifiedCache,
}
unifiedCache.AddListener(searchService)

// All services share the same cached data!
```
