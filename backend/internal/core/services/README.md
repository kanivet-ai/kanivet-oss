# Core Services - Design Principles

## Key Rule: Use Existing Primitives

We have three main infrastructure primitives that MUST be used:

1. **Cache Service** (`cache.Cache`) - For all caching needs
2. **Watcher Service** (`watcher.Service`) - For all K8s watching
3. **WebSocket Hub** (`core.Hub`) - For all real-time updates

## Current Problems to Fix

Looking at the logs, we see:
- **50+ individual watchers** being created for pod statuses
- **3+ second response times** due to API throttling
- **Duplicate caching** in different services

## The Solution: Namespace-Level Architecture

### Before (Wrong)
```
50 Deployments → 50 Pod Watchers → 50 API Watches → Throttling
```

### After (Correct)
```
50 Deployments → 1 Namespace Watcher → 1 API Watch → Fast
```

## Implementation Pattern

### 1. Use Adapters, Not Reimplementations

```go
// WRONG: Reimplementing caching
type Service struct {
    myCache map[string]interface{}  // Don't do this!
}

// CORRECT: Use existing cache
type Service struct {
    cache *cache.Cache  // Reuse existing primitive
}
```

### 2. Watch at Namespace Level

```go
// WRONG: One watcher per resource
for _, deployment := range deployments {
    watcher.StartPodStatusWatch(cluster, ns, deployment.Selector, deployment.UID)
}

// CORRECT: One watcher per namespace
watcher.StartWatch(cluster, "", "v1", "pods", namespace)
```

### 3. Cache at Namespace Level

```go
// WRONG: Cache per resource
cacheKey := fmt.Sprintf("pods:%s:%s", cluster, resourceUID)

// CORRECT: Cache per namespace
cacheKey := fmt.Sprintf("pods:%s:%s:all", cluster, namespace)
```

## Example: Fixed Pod Status Service

```go
func (s *PodStatusService) GetPodStatuses(ctx context.Context, cluster string, resources []map[string]interface{}) {
    // Group by namespace
    byNamespace := groupByNamespace(resources)

    for namespace, nsResources := range byNamespace {
        // ONE cache entry per namespace
        cacheKey := fmt.Sprintf("pods:%s:%s:all", cluster, namespace)

        // Use EXISTING cache service
        allPods := s.cache.GetOrSet(cacheKey, 5*time.Minute, func() {
            // Ensure ONE namespace watcher
            s.ensureNamespaceWatch(cluster, namespace)
            // Get ALL pods in namespace
            return s.k8sClient.ListPods(cluster, namespace)
        })

        // Filter locally for each resource (no API calls!)
        for _, resource := range nsResources {
            matchingPods := filterBySelector(allPods, resource.Selector)
            result[resource.UID] = matchingPods
        }
    }
}
```

## Benefits

1. **Performance**: 50 watchers → 1 watcher per namespace
2. **Response Time**: 3+ seconds → <100ms
3. **No Throttling**: Dramatically fewer API calls
4. **Simple Code**: Just thin adapters over existing services
5. **Maintainable**: No custom implementations to maintain

## Services to Update

1. **PodStatusService** - Use namespace-level watching and caching
2. **SearchService** - Should listen to watcher events, not create own watches
3. **ResourceService** - Should use ResourceCacheAdapter for all queries

## Remember

- **Don't reimplement** - Use existing primitives
- **Don't over-engineer** - Simple adapters are better
- **Watch efficiently** - Namespace level, not resource level
- **Cache smartly** - Share data between services
