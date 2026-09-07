package cache

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type entry struct {
	value interface{}
	exp   int64
}

type inflight struct {
	done chan struct{}
	val  interface{}
	err  error
}

type Cache struct {
	mu       sync.RWMutex
	items    map[string]entry
	fetching map[string]*inflight
	def      time.Duration
}

func New(defaultExpiration, cleanupInterval time.Duration) *Cache {
	c := &Cache{items: make(map[string]entry), fetching: make(map[string]*inflight), def: defaultExpiration}
	if cleanupInterval > 0 {
		go c.janitor(cleanupInterval)
	}
	return c
}

func (c *Cache) janitor(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		now := time.Now().UnixNano()
		c.mu.Lock()
		for k, e := range c.items {
			if e.exp > 0 && now > e.exp {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}

func (c *Cache) GetOrSet(key string, ttl time.Duration, fetcher func() (interface{}, error)) (interface{}, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	c.mu.Lock()
	if f, ok := c.fetching[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.val, f.err
	}
	f := &inflight{done: make(chan struct{})}
	c.fetching[key] = f
	c.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			f.err = fmt.Errorf("cache fetcher panic: %v", r)
			defer panic(r)
		}
		c.mu.Lock()
		delete(c.fetching, key)
		c.mu.Unlock()
		close(f.done)
	}()
	f.val, f.err = fetcher()
	if f.err == nil {
		c.Set(key, f.val, ttl)
	}
	return f.val, f.err
}

func (c *Cache) BuildKey(parts ...string) string {
	return strings.Join(parts, ":")
}

func (c *Cache) Invalidate(pattern string) {
	if strings.HasSuffix(pattern, "*") {
		c.DeleteByPrefix(strings.TrimSuffix(pattern, "*"))
		return
	}
	c.Delete(pattern)
}

func (c *Cache) Clear() {
	c.mu.Lock()
	c.items = make(map[string]entry)
	c.mu.Unlock()
}

func (c *Cache) Delete(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	if ttl == 0 {
		ttl = c.def
	}
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).UnixNano()
	}
	c.mu.Lock()
	c.items[key] = entry{value, exp}
	c.mu.Unlock()
}

func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || (e.exp > 0 && time.Now().UnixNano() > e.exp) {
		return nil, false
	}
	return e.value, true
}

func (c *Cache) DeleteByPrefix(prefix string) {
	c.mu.Lock()
	for k := range c.items {
		if strings.HasPrefix(k, prefix) {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
}
