package cache

import (
	"sync"
	"time"
)

type InvalidationBus struct {
	mu        sync.RWMutex
	listeners map[InvalidationListener]struct{}
}

type InvalidationListener interface {
	OnInvalidate(key string)
}

func NewInvalidationBus() *InvalidationBus {
	return &InvalidationBus{
		listeners: make(map[InvalidationListener]struct{}),
	}
}

func (b *InvalidationBus) Subscribe(listener InvalidationListener) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners[listener] = struct{}{}
}

func (b *InvalidationBus) Unsubscribe(listener InvalidationListener) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.listeners, listener)
}

func (b *InvalidationBus) Invalidate(key string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for listener := range b.listeners {
		listener.OnInvalidate(key)
	}
}

func (b *InvalidationBus) InvalidatePattern(pattern string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for listener := range b.listeners {
		listener.OnInvalidate(pattern)
	}
}

type CacheWithInvalidation struct {
	*Cache
	bus *InvalidationBus
}

func NewCacheWithInvalidation(defaultExpiration, cleanupInterval time.Duration, bus *InvalidationBus) *CacheWithInvalidation {
	c := &CacheWithInvalidation{
		Cache: New(defaultExpiration, cleanupInterval),
		bus:   bus,
	}
	if bus != nil {
		bus.Subscribe(c)
	}
	return c
}

func (c *CacheWithInvalidation) OnInvalidate(pattern string) {
	c.Invalidate(pattern)
}

func (c *CacheWithInvalidation) InvalidateAndNotify(pattern string) {
	c.Invalidate(pattern)
	if c.bus != nil {
		c.bus.Invalidate(pattern)
	}
}
