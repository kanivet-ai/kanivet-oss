package api

import (
	"sync"

	"github.com/kanivet/backend/internal/models"
)

type NavigationService struct {
	mu         sync.RWMutex
	histories  map[string]*models.TabHistory
	maxEntries int
}

func NewNavigationService() *NavigationService {
	return &NavigationService{
		histories:  make(map[string]*models.TabHistory),
		maxEntries: 100,
	}
}

func (s *NavigationService) AddEntry(tabID, clusterID string, entry models.NavigationEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history, exists := s.histories[tabID]
	if !exists {
		history = &models.TabHistory{
			ClusterID: clusterID,
			Entries:   []models.NavigationEntry{},
			Index:     -1,
		}
		s.histories[tabID] = history
	}

	if history.Index < len(history.Entries)-1 {
		history.Entries = history.Entries[:history.Index+1]
	}

	history.Entries = append(history.Entries, entry)

	if len(history.Entries) > s.maxEntries {
		history.Entries = history.Entries[len(history.Entries)-s.maxEntries:]
	}

	history.Index = len(history.Entries) - 1
}

func (s *NavigationService) GetHistory(tabID string) *models.TabHistory {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history, exists := s.histories[tabID]
	if !exists {
		return nil
	}

	historyCopy := &models.TabHistory{
		ClusterID: history.ClusterID,
		Entries:   make([]models.NavigationEntry, len(history.Entries)),
		Index:     history.Index,
	}
	copy(historyCopy.Entries, history.Entries)

	return historyCopy
}

func (s *NavigationService) NavigateBack(tabID string) *models.NavigationEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	history, exists := s.histories[tabID]
	if !exists || history.Index <= 0 {
		return nil
	}

	history.Index--
	entry := history.Entries[history.Index]
	return &entry
}

func (s *NavigationService) NavigateForward(tabID string) *models.NavigationEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	history, exists := s.histories[tabID]
	if !exists || history.Index >= len(history.Entries)-1 {
		return nil
	}

	history.Index++
	entry := history.Entries[history.Index]
	return &entry
}

func (s *NavigationService) ClearHistory(tabID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.histories, tabID)
}
