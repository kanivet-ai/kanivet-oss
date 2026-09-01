package cache

import (
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
)

type Cache struct {
	store *gocache.Cache
}

func New(defaultExpiration, cleanupInterval time.Duration) *Cache {
	return &Cache{
		store: gocache.New(defaultExpiration, cleanupInterval),
	}
}

func (c *Cache) GetOrSet(key string, ttl time.Duration, fetcher func() (interface{}, error)) (interface{}, error) {
	if cached, found := c.store.Get(key); found {
		return cached, nil
	}

	data, err := fetcher()
	if err != nil {
		return nil, err
	}

	c.store.Set(key, data, ttl)
	return data, nil
}

func (c *Cache) BuildKey(parts ...string) string {
	return strings.Join(parts, ":")
}

func (c *Cache) Invalidate(pattern string) {
	// Support both exact keys and prefix patterns
	if strings.HasSuffix(pattern, "*") {
		// Prefix matching for patterns like "pod-status:cluster:*"
		prefix := strings.TrimSuffix(pattern, "*")
		for key := range c.store.Items() {
			if strings.HasPrefix(key, prefix) {
				c.store.Delete(key)
			}
		}
	} else {
		// Exact key matching
		c.store.Delete(pattern)
	}
}

func (c *Cache) Clear() {
	c.store.Flush()
}

// Delete removes a specific cache entry
func (c *Cache) Delete(key string) {
	c.store.Delete(key)
}

// Set stores a value in the cache with the specified TTL
func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	c.store.Set(key, value, ttl)
}

// Get returns a value if present and not expired.
func (c *Cache) Get(key string) (interface{}, bool) {
	return c.store.Get(key)
}

// DeleteByPrefix removes all cache entries with keys starting with the given prefix
func (c *Cache) DeleteByPrefix(prefix string) {
	for key := range c.store.Items() {
		if strings.HasPrefix(key, prefix) {
			c.store.Delete(key)
		}
	}
}
