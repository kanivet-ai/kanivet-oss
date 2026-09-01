package search

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/search/storage"
	"github.com/kanivet/backend/internal/utils"
)

func (s *Service) Search(query string, options SearchOptions) ([]SearchResult, error) {
	searchQuery := s.parseQuery(query, options)
	if searchQuery.Cluster != "" {
		s.mu.Lock()
		s.activeClusters[searchQuery.Cluster] = time.Now()
		s.mu.Unlock()
	}
	cacheKey := s.buildSearchCacheKey(query, searchQuery)
	if cached, err := s.cache.GetOrSet(cacheKey, time.Duration(s.config.SearchCacheTTL)*time.Second, func() (interface{}, error) {
		return s.searchWithKinds(searchQuery)
	}); err == nil {
		results := cached.([]SearchResult)
		return results, nil
	}
	results, err := s.searchWithKinds(searchQuery)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	return results, nil
}

func (s *Service) searchWithKinds(query SearchQuery) ([]SearchResult, error) {
	results, err := s.index.Search(query)
	if err != nil {
		return nil, err
	}
	desiredResults := query.Limit
	if desiredResults == 0 {
		desiredResults = 20
	}
	if s.db != nil && s.index.AtCapacity() && len(results) < desiredResults {
		dbResults := s.searchDBFallback(query, desiredResults-len(results), results)
		results = append(results, dbResults...)
	}
	if query.Text == "" {
		return results, nil
	}
	var kindDefResults []SearchResult
	var regularResults []SearchResult
	for _, result := range results {
		if result.Resource.Kind == "KindDefinition" {
			kindDefResults = append(kindDefResults, result)
		} else {
			regularResults = append(regularResults, result)
		}
	}
	if len(kindDefResults) > 0 {
		sort.Slice(kindDefResults, func(i, j int) bool {
			return kindDefResults[i].Score > kindDefResults[j].Score
		})
		combinedResults := append(kindDefResults, regularResults...)
		if query.Limit > 0 && len(combinedResults) > query.Limit {
			combinedResults = combinedResults[:query.Limit]
		}
		return combinedResults, nil
	}
	log.Printf("[SEARCH] No indexed kinds found for query '%s', deriving kinds from index", query.Text)
	type KindInfo struct {
		Namespaced bool
		Group      string
		Version    string
	}
	clusterKindInfo := make(map[string]map[string]KindInfo)
	for _, result := range regularResults {
		if clusterKindInfo[result.Resource.Cluster] == nil {
			clusterKindInfo[result.Resource.Cluster] = make(map[string]KindInfo)
		}
		clusterKindInfo[result.Resource.Cluster][result.Resource.Kind] = KindInfo{
			Namespaced: result.Resource.Namespace != "",
			Group:      result.Resource.Group,
			Version:    result.Resource.Version,
		}
	}
	var kindClusters []string
	if query.Cluster != "" {
		kindClusters = []string{query.Cluster}
	}
	queryLower := strings.ToLower(query.Text)
	for _, info := range s.index.FindKindsLike(queryLower, kindClusters, 100) {
		if clusterKindInfo[info.Cluster] == nil {
			clusterKindInfo[info.Cluster] = make(map[string]KindInfo)
		}
		clusterKindInfo[info.Cluster][info.Kind] = KindInfo{
			Namespaced: info.Namespaced,
			Group:      info.Group,
			Version:    info.Version,
		}
	}
	kindResults := []SearchResult{}
	for _, cluster := range getMapKeys(clusterKindInfo) {
		for kind, kindInfo := range clusterKindInfo[cluster] {
			kindLower := strings.ToLower(kind)
			score := 0.0
			if strings.EqualFold(kind, query.Text) {
				score = 100.0
			} else if strings.HasPrefix(kindLower, queryLower) {
				score = 80.0
			} else if strings.Contains(kindLower, queryLower) {
				score = 60.0
			} else {
				continue
			}
			apiVersion := kindInfo.Version
			if kindInfo.Group != "" {
				apiVersion = kindInfo.Group + "/" + kindInfo.Version
			}
			kindResult := SearchResult{
				Resource: SearchableResource{
					ID:         fmt.Sprintf("kind:%s:%s:%s:%s", cluster, kindInfo.Group, kindInfo.Version, kind),
					Cluster:    cluster,
					Kind:       "KindDefinition",
					Name:       kind,
					Namespace:  "",
					Category:   "Kind",
					APIVersion: apiVersion,
					Group:      kindInfo.Group,
					Version:    kindInfo.Version,
					Labels: map[string]string{
						"resource-kind": kind,
						"resource-name": utils.PluralizeKind(kind),
						"namespaced":    fmt.Sprintf("%t", kindInfo.Namespaced),
						"group":         kindInfo.Group,
						"version":       kindInfo.Version,
					},
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				Score: score,
				Matches: []SearchMatch{
					{Field: "kind", Value: kind},
				},
			}
			kindResults = append(kindResults, kindResult)
		}
	}
	sort.Slice(kindResults, func(i, j int) bool {
		return kindResults[i].Score > kindResults[j].Score
	})
	combinedResults := append(kindResults, regularResults...)
	if query.Limit > 0 && len(combinedResults) > query.Limit {
		combinedResults = combinedResults[:query.Limit]
	}
	return combinedResults, nil
}

func (s *Service) GetRecentSearches(limit int) ([]RecentResource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit > len(s.recentSearches) {
		limit = len(s.recentSearches)
	}
	result := make([]RecentResource, limit)
	copy(result, s.recentSearches[:limit])
	return result, nil
}

func (s *Service) SaveSearchHistory(resource RecentResource) error {
	s.saveToRecentSearches(resource)
	return nil
}

func (s *Service) parseQuery(text string, options SearchOptions) SearchQuery {
	query := SearchQuery{
		Text:       text,
		Filters:    make(map[string]string),
		Namespaces: options.Namespaces,
		Kinds:      options.Kinds,
		Labels:     make(map[string]string),
		Limit:      options.Limit,
		Offset:     options.Offset,
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	if len(options.Clusters) > 0 {
		clusters := append([]string(nil), options.Clusters...)
		sort.Strings(clusters)
		query.Cluster = clusters[0]
	}
	return query
}

func (s *Service) buildSearchCacheKey(text string, q SearchQuery) string {
	global, clusterVersion := s.searchVersions(q.Cluster)
	namespaces := append([]string(nil), q.Namespaces...)
	kinds := append([]string(nil), q.Kinds...)
	sort.Strings(namespaces)
	sort.Strings(kinds)
	return s.cache.BuildKey("search",
		fmt.Sprintf("v%d.%d", global, clusterVersion),
		q.Cluster,
		strings.ToLower(strings.TrimSpace(text)),
		strings.Join(namespaces, ","),
		strings.Join(kinds, ","),
		fmt.Sprintf("%d.%d", q.Limit, q.Offset),
	)
}

func (s *Service) saveToRecentSearches(resource RecentResource) {
	if resource.Name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.recentSearches {
		if r.Name == resource.Name && r.Kind == resource.Kind && r.Namespace == resource.Namespace && r.Cluster == resource.Cluster {
			s.recentSearches = append(s.recentSearches[:i], s.recentSearches[i+1:]...)
			break
		}
	}
	s.recentSearches = append([]RecentResource{resource}, s.recentSearches...)
	if len(s.recentSearches) > s.config.MaxRecentSearches {
		s.recentSearches = s.recentSearches[:s.config.MaxRecentSearches]
	}
}

func (s *Service) searchDBFallback(query SearchQuery, limit int, existingResults []SearchResult) []SearchResult {
	if s.db == nil || limit <= 0 {
		return nil
	}
	existingIDs := make(map[string]bool, len(existingResults))
	for _, r := range existingResults {
		existingIDs[r.Resource.ID] = true
	}
	var clusters []string
	if query.Cluster != "" {
		clusters = []string{query.Cluster}
	}
	dbResources, _, err := s.db.SearchResources(query.Text, clusters, limit*2, 0)
	if err != nil {
		log.Printf("[SEARCH] DB fallback search failed: %v", err)
		return nil
	}
	var dbResults []SearchResult
	for _, dbRes := range dbResources {
		if existingIDs[dbRes.ResourceID] {
			continue
		}
		if dbRes.Kind == "KindDefinition" {
			continue
		}
		searchable := s.convertDBToSearchable(dbRes)
		if len(query.Namespaces) > 0 {
			found := false
			for _, ns := range query.Namespaces {
				if searchable.Namespace == ns {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if len(query.Kinds) > 0 {
			found := false
			for _, kind := range query.Kinds {
				if strings.EqualFold(searchable.Kind, kind) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		dbResults = append(dbResults, SearchResult{
			Resource: searchable,
			Score:    0.5,
			Matches:  []SearchMatch{{Field: "db_fallback", Value: query.Text}},
		})
		go func(res storage.SearchableResource) {
			if err := s.index.Index(res); err != nil {
				log.Printf("[SEARCH] Failed to promote DB result to memory: %v", err)
			}
		}(searchable)
		if len(dbResults) >= limit {
			break
		}
	}
	if len(dbResults) > 0 {
		log.Printf("[SEARCH] DB fallback returned %d additional results for query '%s'", len(dbResults), query.Text)
	}
	return dbResults
}
