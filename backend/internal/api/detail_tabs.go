package api

import (
	"fmt"
	"sync"
)

const maxDetailTabStates = 500

type DetailTabState struct {
	ActiveTab string `json:"activeTab"`
}

type DetailTabManager struct {
	states  map[string]*DetailTabState
	seq     map[string]uint64
	nextSeq uint64
	mu      sync.RWMutex
}

func NewDetailTabManager() *DetailTabManager {
	return &DetailTabManager{
		states: make(map[string]*DetailTabState),
		seq:    make(map[string]uint64),
	}
}

func (m *DetailTabManager) getKey(cluster, group, version, kind, namespace, name string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s", cluster, group, version, kind, namespace, name)
}

func (m *DetailTabManager) GetTabState(cluster, group, version, kind, namespace, name string) *DetailTabState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := m.getKey(cluster, group, version, kind, namespace, name)
	if state, exists := m.states[key]; exists {
		return state
	}
	return &DetailTabState{ActiveTab: "pretty"}
}

func (m *DetailTabManager) SetTabState(cluster, group, version, kind, namespace, name, activeTab string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := m.getKey(cluster, group, version, kind, namespace, name)
	if _, exists := m.states[key]; !exists && len(m.states) >= maxDetailTabStates {
		var oldestKey string
		var oldestSeq uint64
		first := true
		for k, s := range m.seq {
			if first || s < oldestSeq {
				oldestKey, oldestSeq, first = k, s, false
			}
		}
		delete(m.states, oldestKey)
		delete(m.seq, oldestKey)
	}
	m.nextSeq++
	m.states[key] = &DetailTabState{ActiveTab: activeTab}
	m.seq[key] = m.nextSeq
}
