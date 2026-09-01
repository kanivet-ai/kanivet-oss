package ports

import (
	"context"
	"time"
)

// Cache defines the interface for caching operations
type Cache interface {
	// Get retrieves a value from the cache
	Get(ctx context.Context, key string) (interface{}, bool)

	// GetWithTTL retrieves a value and its remaining TTL
	GetWithTTL(ctx context.Context, key string) (interface{}, time.Duration, bool)

	// Set stores a value in the cache with a TTL
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error

	// SetNX stores a value only if the key doesn't exist
	SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error)

	// Delete removes a value from the cache
	Delete(ctx context.Context, key string) error

	// DeleteMany removes multiple values from the cache
	DeleteMany(ctx context.Context, keys []string) error

	// Clear removes all values from the cache
	Clear(ctx context.Context) error

	// Exists checks if a key exists in the cache
	Exists(ctx context.Context, key string) bool

	// Expire updates the TTL for a key
	Expire(ctx context.Context, key string, ttl time.Duration) error

	// TTL returns the remaining TTL for a key
	TTL(ctx context.Context, key string) (time.Duration, error)

	// Keys returns all keys matching a pattern
	Keys(ctx context.Context, pattern string) ([]string, error)

	// Size returns the number of items in the cache
	Size(ctx context.Context) (int64, error)

	// Stats returns cache statistics
	Stats(ctx context.Context) (*CacheStats, error)
}

// TypedCache provides type-safe cache operations
type TypedCache[T any] interface {
	// Get retrieves a typed value from the cache
	Get(ctx context.Context, key string) (T, bool)

	// Set stores a typed value in the cache
	Set(ctx context.Context, key string, value T, ttl time.Duration) error

	// GetOrSet retrieves a value or sets it if not found
	GetOrSet(ctx context.Context, key string, factory func() (T, error), ttl time.Duration) (T, error)

	// Delete removes a value from the cache
	Delete(ctx context.Context, key string) error
}

// CacheStats represents cache statistics
type CacheStats struct {
	Hits         int64     // Number of cache hits
	Misses       int64     // Number of cache misses
	Sets         int64     // Number of set operations
	Deletes      int64     // Number of delete operations
	Evictions    int64     // Number of evictions
	Size         int64     // Current number of items
	MemoryUsage  int64     // Memory usage in bytes
	LastEviction time.Time // Last eviction time
}

// CacheKeyBuilder helps build consistent cache keys
type CacheKeyBuilder interface {
	// ForResource builds a cache key for a resource
	ForResource(cluster, namespace, group, version, kind, name string) string

	// ForResourceList builds a cache key for a resource list
	ForResourceList(cluster, namespace, group, version, kind string, filters map[string]string) string

	// ForSearch builds a cache key for a search query
	ForSearch(query string, filters map[string]interface{}) string

	// ForCluster builds a cache key for cluster information
	ForCluster(clusterID string) string

	// WithPrefix adds a prefix to any key
	WithPrefix(prefix, key string) string
}

// DefaultCacheKeyBuilder implements CacheKeyBuilder
type DefaultCacheKeyBuilder struct{}

// ForResource builds a cache key for a resource
func (b DefaultCacheKeyBuilder) ForResource(cluster, namespace, group, version, kind, name string) string {
	if namespace == "" {
		return "resource:" + cluster + "/" + group + "/" + version + "/" + kind + "/" + name
	}
	return "resource:" + cluster + "/" + namespace + "/" + group + "/" + version + "/" + kind + "/" + name
}

// ForResourceList builds a cache key for a resource list
func (b DefaultCacheKeyBuilder) ForResourceList(cluster, namespace, group, version, kind string, filters map[string]string) string {
	key := "list:" + cluster + "/" + namespace + "/" + group + "/" + version + "/" + kind
	// Add sorted filters to ensure consistent keys
	for k, v := range filters {
		key += "/" + k + "=" + v
	}
	return key
}

// ForSearch builds a cache key for a search query
func (b DefaultCacheKeyBuilder) ForSearch(query string, filters map[string]interface{}) string {
	key := "search:" + query
	// Add filters (in practice, you'd want to sort these for consistency)
	for k, v := range filters {
		key += "/" + k + "=" + formatValue(v)
	}
	return key
}

// ForCluster builds a cache key for cluster information
func (b DefaultCacheKeyBuilder) ForCluster(clusterID string) string {
	return "cluster:" + clusterID
}

// WithPrefix adds a prefix to any key
func (b DefaultCacheKeyBuilder) WithPrefix(prefix, key string) string {
	return prefix + ":" + key
}

// Helper function to format values consistently
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []string:
		return "[" + joinStrings(val, ",") + "]"
	default:
		return ""
	}
}

// Helper function to join strings
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// CacheOptions represents cache configuration options
type CacheOptions struct {
	DefaultTTL      time.Duration // Default TTL for cached items
	MaxSize         int64         // Maximum number of items
	MaxMemory       int64         // Maximum memory usage in bytes
	EvictionPolicy  string        // LRU, LFU, etc.
	EnableStats     bool          // Enable statistics collection
	CleanupInterval time.Duration // Cleanup interval for expired items
}

// CacheEvictionPolicy represents the eviction policy
type CacheEvictionPolicy string

const (
	EvictionLRU    CacheEvictionPolicy = "lru"    // Least Recently Used
	EvictionLFU    CacheEvictionPolicy = "lfu"    // Least Frequently Used
	EvictionFIFO   CacheEvictionPolicy = "fifo"   // First In First Out
	EvictionRandom CacheEvictionPolicy = "random" // Random eviction
)
