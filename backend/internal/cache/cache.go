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
	// invalidated marks a stale-while-revalidate entry expired by Invalidate
	// rather than by its ttl: the next read waits for a fresh value instead of
	// being served this one.
	invalidated bool
}

type inflight struct {
	done chan struct{}
	val  interface{}
	err  error
	// superseded is set, under Cache.mu, when the key is invalidated or
	// deleted while the fetch runs: its value may predate the change, so it is
	// returned to the callers already waiting but never stored.
	superseded bool
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
	return c.fetch(key, fetcher, func(v interface{}) entry { return c.newEntry(v, ttl) })
}

// GetOrSetSWR is GetOrSet with stale-while-revalidate: for up to maxStale
// after its ttl a value is still returned at once, and one background fetch
// replaces it. Callers wait for the fetch only when nothing usable is cached.
// An entry expired by Invalidate is not served stale: the change that
// invalidated it must show on the next read, so that read waits for the fetch
// and falls back to the previous value only if the fetch fails.
// The fetcher of a background refresh outlives the request that started it,
// so it must not depend on that request's context.
func (c *Cache) GetOrSetSWR(key string, ttl, maxStale time.Duration, fetcher func() (interface{}, error)) (interface{}, error) {
	store := func(v interface{}) entry { return swrEntry(v, ttl, maxStale) }
	now := time.Now().UnixNano()
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if ok && (e.exp == 0 || now <= e.exp) {
		return e.value, nil
	}
	if ok && now <= e.staleUntil {
		if e.invalidated {
			v, err := c.fetch(key, fetcher, store)
			if err != nil {
				log.Printf("[Cache] refresh of invalidated %s failed, serving the previous value: %v", key, err)
				return e.value, nil
			}
			return v, nil
		}
		c.refreshInBackground(key, fetcher, store)
		return e.value, nil
	}
	return c.fetch(key, fetcher, store)
}

// fetch runs fetcher once per key at a time: concurrent callers for the same
// key wait for the running fetch and share its result. A fetch superseded by
// an invalidation is not joined: its value may predate the change, so a new
// fetch takes its place.
func (c *Cache) fetch(key string, fetcher func() (interface{}, error), store func(interface{}) entry) (interface{}, error) {
	c.mu.Lock()
	if f, ok := c.fetching[key]; ok && !f.superseded {
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
		c.store(key, f, store(f.val))
	}
	return f.val, f.err
}

// store saves e under key unless f was superseded while it ran.
func (c *Cache) store(key string, f *inflight, e entry) {
	c.mu.Lock()
	if !f.superseded {
		c.items[key] = e
	}
	c.mu.Unlock()
}

// refreshInBackground starts a fetch for key unless one is already running. A
// failed refresh keeps the stale value; the next read past ttl retries it.
func (c *Cache) refreshInBackground(key string, fetcher func() (interface{}, error), store func(interface{}) entry) {
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
		c.store(key, f, store(f.val))
	}()
}

func (c *Cache) finish(key string, f *inflight) {
	c.mu.Lock()
	if c.fetching[key] == f {
		delete(c.fetching, key)
	}
	c.mu.Unlock()
	close(f.done)
}

func swrEntry(value interface{}, ttl, maxStale time.Duration) entry {
	now := time.Now()
	return entry{value: value, exp: now.Add(ttl).UnixNano(), staleUntil: now.Add(ttl + maxStale).UnixNano()}
}

func (c *Cache) newEntry(value interface{}, ttl time.Duration) entry {
	if ttl == 0 {
		ttl = c.def
	}
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).UnixNano()
	}
	return entry{value: value, exp: exp}
}

// supersedeLocked marks the fetches running for keys matching match, so their
// possibly pre-change values are not stored.
func (c *Cache) supersedeLocked(match func(string) bool) {
	for k, f := range c.fetching {
		if match(k) {
			f.superseded = true
		}
	}
}

func (c *Cache) BuildKey(parts ...string) string {
	return strings.Join(parts, ":")
}

// Invalidate drops the entries matching pattern, a key or a prefix ending in
// "*". An entry stored by GetOrSetSWR is kept, marked invalidated, so the
// next read can fall back to it if its refresh fails; that read still waits
// for the refresh. Delete and DeleteByPrefix always drop. Either way a fetch
// already running for a matching key does not store its result.
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
		c.supersedeLocked(func(k string) bool { return strings.HasPrefix(k, prefix) })
		return
	}
	if e, ok := c.items[pattern]; ok {
		c.invalidateLocked(pattern, e, now)
	}
	c.supersedeLocked(func(k string) bool { return k == pattern })
}

func (c *Cache) invalidateLocked(key string, e entry, now int64) {
	if now < e.staleUntil {
		e.exp = now - 1
		e.invalidated = true
		c.items[key] = e
		return
	}
	delete(c.items, key)
}

func (c *Cache) Clear() {
	c.mu.Lock()
	c.items = make(map[string]entry)
	c.supersedeLocked(func(string) bool { return true })
	c.mu.Unlock()
}

func (c *Cache) Delete(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.supersedeLocked(func(k string) bool { return k == key })
	c.mu.Unlock()
}

func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	e := c.newEntry(value, ttl)
	c.mu.Lock()
	c.items[key] = e
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
	c.supersedeLocked(func(k string) bool { return strings.HasPrefix(k, prefix) })
	c.mu.Unlock()
}
