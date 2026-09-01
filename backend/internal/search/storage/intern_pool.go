package storage

import (
	"sync"
)

type StringInternPool struct {
	strings []string
	lookup  map[string]uint32
	mu      sync.RWMutex
}

func NewStringInternPool() *StringInternPool {
	pool := &StringInternPool{
		strings: make([]string, 1, 1024),
		lookup:  make(map[string]uint32, 1024),
	}
	pool.strings[0] = ""
	return pool
}

func (p *StringInternPool) Intern(s string) uint32 {
	if s == "" {
		return 0
	}
	p.mu.RLock()
	if idx, ok := p.lookup[s]; ok {
		p.mu.RUnlock()
		return idx
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, ok := p.lookup[s]; ok {
		return idx
	}
	idx := uint32(len(p.strings))
	p.strings = append(p.strings, s)
	p.lookup[s] = idx
	return idx
}

func (p *StringInternPool) Lookup(s string) (uint32, bool) {
	if s == "" {
		return 0, true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	idx, ok := p.lookup[s]
	return idx, ok
}

func (p *StringInternPool) Get(idx uint32) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if int(idx) >= len(p.strings) {
		return ""
	}
	return p.strings[idx]
}

func (p *StringInternPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.strings)
}

type InternPools struct {
	Clusters   *StringInternPool
	Kinds      *StringInternPool
	Namespaces *StringInternPool
	Groups     *StringInternPool
	Versions   *StringInternPool
	Names      *StringInternPool
	IDs        *StringInternPool
}

func NewInternPools() *InternPools {
	return &InternPools{
		Clusters:   NewStringInternPool(),
		Kinds:      NewStringInternPool(),
		Namespaces: NewStringInternPool(),
		Groups:     NewStringInternPool(),
		Versions:   NewStringInternPool(),
		Names:      NewStringInternPool(),
		IDs:        NewStringInternPool(),
	}
}
