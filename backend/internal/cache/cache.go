package cache

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type entry struct {
	value interface{}
	exp   int64
	// staleUntil, set by GetOrSetSWR, keeps the value servable past exp while
	// a background fetch replaces it.
	staleUntil int64
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
			if e.exp > 0 && now > e.exp && now > e.staleUntil {
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
	return c.fetch(key, fetcher, func(v interface{}) { c.Set(key, v, ttl) })
}

// GetOrSetSWR is GetOrSet with stale-while-revalidate: for up to maxStale
// after its ttl a value is still returned at once, and one background fetch
// replaces it. Callers wait for the fetch only when nothing usable is cached.
// The fetcher of a background refresh outlives the request that started it,
// so it must not depend on that request's context.
func (c *Cache) GetOrSetSWR(key string, ttl, maxStale time.Duration, fetcher func() (interface{}, error)) (interface{}, error) {
	store := func(v interface{}) { c.setSWR(key, v, ttl, maxStale) }
	now := time.Now().UnixNano()
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if ok && (e.exp == 0 || now <= e.exp) {
		return e.value, nil
	}
	if ok && now <= e.staleUntil {
		c.refreshInBackground(key, fetcher, store)
		return e.value, nil
	}
	return c.fetch(key, fetcher, store)
}

// fetch runs fetcher once per key at a time: concurrent callers for the same
// key wait for the running fetch and share its result.
func (c *Cache) fetch(key string, fetcher func() (interface{}, error), store func(interface{})) (interface{}, error) {
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
		c.finish(key, f)
	}()
	f.val, f.err = fetcher()
	if f.err == nil {
		store(f.val)
	}
	return f.val, f.err
}

// refreshInBackground starts a fetch for key unless one is already running. A
// failed refresh keeps the stale value; the next read past ttl retries it.
func (c *Cache) refreshInBackground(key string, fetcher func() (interface{}, error), store func(interface{})) {
	c.mu.Lock()
	if _, ok := c.fetching[key]; ok {
		c.mu.Unlock()
		return
	}
	f := &inflight{done: make(chan struct{})}
	c.fetching[key] = f
	c.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				f.err = fmt.Errorf("cache fetcher panic: %v", r)
				log.Printf("[Cache] background refresh of %s panicked: %v", key, r)
			}
			c.finish(key, f)
		}()
		f.val, f.err = fetcher()
		if f.err != nil {
			log.Printf("[Cache] background refresh of %s failed, serving the stale value: %v", key, f.err)
			return
		}
		store(f.val)
	}()
}

func (c *Cache) finish(key string, f *inflight) {
	c.mu.Lock()
	delete(c.fetching, key)
	c.mu.Unlock()
	close(f.done)
}

func (c *Cache) setSWR(key string, value interface{}, ttl, maxStale time.Duration) {
	now := time.Now()
	c.mu.Lock()
	c.items[key] = entry{value: value, exp: now.Add(ttl).UnixNano(), staleUntil: now.Add(ttl + maxStale).UnixNano()}
	c.mu.Unlock()
}

func (c *Cache) BuildKey(parts ...string) string {
	return strings.Join(parts, ":")
}

// Invalidate drops the entries matching pattern, a key or a prefix ending in
// "*". An entry stored by GetOrSetSWR is expired instead of dropped, so the
// next read still answers at once and refreshes it in the background: watch
// events invalidate whole clusters' entries many times a minute. Delete and
// DeleteByPrefix always drop.
func (c *Cache) Invalidate(pattern string) {
	now := time.Now().UnixNano()
	c.mu.Lock()
	defer c.mu.Unlock()
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		for k, e := range c.items {
			if strings.HasPrefix(k, prefix) {
				c.invalidateLocked(k, e, now)
			}
		}
		return
	}
	if e, ok := c.items[pattern]; ok {
		c.invalidateLocked(pattern, e, now)
	}
}

func (c *Cache) invalidateLocked(key string, e entry, now int64) {
	if now < e.staleUntil {
		e.exp = now - 1
		c.items[key] = e
		return
	}
	delete(c.items, key)
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
	c.items[key] = entry{value: value, exp: exp}
	c.mu.Unlock()
}

// Replace sets key to value only while it still holds old, expired or not,
// so a slow refresh cannot overwrite an entry deleted or replaced since it
// began. old must be comparable.
func (c *Cache) Replace(key string, old, value interface{}, ttl time.Duration) bool {
	if ttl == 0 {
		ttl = c.def
	}
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).UnixNano()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; !ok || e.value != old {
		return false
	}
	c.items[key] = entry{value: value, exp: exp}
	return true
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
