package search

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/search/storage"
	"github.com/kanivet/backend/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var smartIndexingDelay = 30 * time.Second

func (s *Service) IndexCluster(cluster string) error {
	s.mu.Lock()
	s.activeClusters[cluster] = time.Now()
	s.mu.Unlock()
	s.indexingMu.Lock()
	if s.activeIndexing[cluster] {
		s.indexingMu.Unlock()
		return nil
	}
	s.activeIndexing[cluster] = true
	s.indexingMu.Unlock()
	go func() {
		defer func() {
			s.indexingMu.Lock()
			delete(s.activeIndexing, cluster)
			s.indexingMu.Unlock()
		}()
		log.Printf("[SEARCH] Starting indexing for cluster: %s", cluster)
		if err := s.indexResourceKinds(cluster); err != nil {
			log.Printf("[SEARCH] Failed to index resource kinds for cluster %s: %v", cluster, err)
		}
		// Defer the full LIST sweep so cold cluster opens keep bandwidth for
		// interactive views; watchers keep already-indexed kinds fresh meanwhile.
		select {
		case <-s.stopChan:
			return
		case <-time.After(smartIndexingDelay):
		}
		s.performSmartIndexing(cluster)
		if s.onIndexingComplete != nil {
			s.onIndexingComplete(cluster)
		}
	}()
	return nil
}

func (s *Service) IndexClusterKindsOnly(cluster string) error {
	s.mu.Lock()
	s.activeClusters[cluster] = time.Now()
	s.mu.Unlock()
	go func() {
		if err := s.indexResourceKinds(cluster); err != nil {
			log.Printf("[SEARCH] Failed to index resource kinds for cluster %s: %v", cluster, err)
		}
	}()
	return nil
}

func (s *Service) IndexResourceType(cluster, group, version, kind string) error {
	topic := fmt.Sprintf("items:%s:%s:%s:%s:", cluster, group, version, kind)
	s.mu.Lock()
	s.watchedTopics[topic] = true
	s.mu.Unlock()
	log.Printf("Search service ready to receive events for %s/%s in cluster %s", group, kind, cluster)
	return nil
}

func (s *Service) GetIndexingStatus(cluster string) (*IndexerStatus, error) {
	s.indexingMu.Lock()
	defer s.indexingMu.Unlock()
	if status, ok := s.indexingStatus[cluster]; ok {
		progress := 0
		effectiveTotal := status.TotalResources - status.FailedResources
		if effectiveTotal > 0 {
			progress = (status.IndexedResources * 100) / effectiveTotal
		} else if status.TotalResources > 0 {
			progress = 100
		}
		return &IndexerStatus{
			Active:         !status.IsComplete,
			CurrentStatus:  fmt.Sprintf("Indexed %d/%d resources", status.IndexedResources, status.TotalResources),
			LastUpdateTime: status.StartedAt,
			Progress:       progress,
		}, nil
	}
	if clusterResources, ok := s.indexedResources[cluster]; ok && len(clusterResources) > 0 {
		return &IndexerStatus{
			Active:         false,
			CurrentStatus:  "Indexing complete",
			LastUpdateTime: time.Now(),
			Progress:       100,
		}, nil
	}
	return &IndexerStatus{
		Active:         false,
		CurrentStatus:  "Not indexed",
		LastUpdateTime: time.Now(),
		Progress:       0,
	}, nil
}

func (s *Service) GetAllIndexingStatuses() map[string]*IndexingStatus {
	s.indexingMu.Lock()
	defer s.indexingMu.Unlock()
	result := make(map[string]*IndexingStatus)
	for cluster, status := range s.indexingStatus {
		statusCopy := *status
		result[cluster] = &statusCopy
	}
	return result
}

func (s *Service) IsResourceIndexed(cluster, group, version, kind string) bool {
	s.indexingMu.Lock()
	defer s.indexingMu.Unlock()
	if clusterResources, ok := s.indexedResources[cluster]; ok {
		resourceKey := fmt.Sprintf("%s/%s/%s", group, version, kind)
		_, indexed := clusterResources[resourceKey]
		return indexed
	}
	return false
}

func (s *Service) GetResourceVersion(cluster, group, version, kind string) string {
	s.indexingMu.Lock()
	defer s.indexingMu.Unlock()
	if clusterVersions, ok := s.resourceVersions[cluster]; ok {
		resourceKey := fmt.Sprintf("%s/%s/%s", group, version, kind)
		return clusterVersions[resourceKey]
	}
	return ""
}

func (s *Service) SetOnIndexingComplete(callback func(cluster string)) {
	s.onIndexingComplete = callback
}

func (s *Service) loadPersistedIndexAsync() {
	if s.db == nil {
		return
	}
	log.Printf("[SEARCH] Starting async load of persisted search index from database...")
	var totalCount int64
	if err := s.db.Model(&db.SearchableResource{}).Count(&totalCount).Error; err != nil {
		log.Printf("[SEARCH] Failed to count persisted resources: %v", err)
		return
	}
	if totalCount == 0 {
		log.Printf("[SEARCH] No resources to load from database")
		return
	}
	log.Printf("[SEARCH] Found %d resources to load from database (search available immediately)", totalCount)
	newData := storage.NewIndexDataWithCapacity(int(totalCount))
	numWorkers := 2
	dbBatchSize := 2000
	type workItem struct {
		resources []db.SearchableResource
	}
	workChan := make(chan workItem, numWorkers*2)
	resultChan := make(chan []storage.PreparedResource, numWorkers*2)
	doneChan := make(chan struct{})
	var indexed int64
	var kindDefCount int64
	clusterVersionMap := make(map[string]map[string]time.Time)
	var clusterMapMu sync.Mutex
	startTime := time.Now()
	var workerWg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for work := range workChan {
				prepared := make([]storage.PreparedResource, 0, len(work.resources))
				for _, dbResource := range work.resources {
					searchable := s.convertDBToSearchable(dbResource)
					prepared = append(prepared, storage.PrepareResource(searchable))
				}
				resultChan <- prepared
			}
		}()
	}
	go func() {
		lastLogTime := time.Now()
		batchCount := 0
		for prepared := range resultChan {
			storage.BatchIndexToDataFast(newData, prepared)
			atomic.AddInt64(&indexed, int64(len(prepared)))
			batchCount++
			if batchCount%10 == 0 {
				runtime.Gosched()
			}
			clusterMapMu.Lock()
			for _, p := range prepared {
				r := p.Resource
				if r.Kind == "KindDefinition" {
					atomic.AddInt64(&kindDefCount, 1)
				} else {
					if clusterVersionMap[r.Cluster] == nil {
						clusterVersionMap[r.Cluster] = make(map[string]time.Time)
					}
					resourceKey := fmt.Sprintf("%s/%s/%s", r.Group, r.Version, utils.PluralizeKind(r.Kind))
					if r.UpdatedAt.After(clusterVersionMap[r.Cluster][resourceKey]) {
						clusterVersionMap[r.Cluster][resourceKey] = r.UpdatedAt
					}
				}
			}
			clusterMapMu.Unlock()
			if time.Since(lastLogTime) > 2*time.Second {
				current := atomic.LoadInt64(&indexed)
				progress := float64(current) / float64(totalCount) * 100
				log.Printf("[SEARCH] Loading index: %d/%d resources (%.1f%%) - search available now", current, totalCount, progress)
				lastLogTime = time.Now()
			}
		}
		close(doneChan)
	}()
	offset := 0
	for offset < int(totalCount) {
		var resources []db.SearchableResource
		if err := s.db.Offset(offset).Limit(dbBatchSize).Find(&resources).Error; err != nil {
			log.Printf("[SEARCH] Failed to load batch at offset %d: %v", offset, err)
			break
		}
		if len(resources) == 0 {
			break
		}
		workChan <- workItem{resources: resources}
		offset += dbBatchSize
		runtime.Gosched()
		if offset%(dbBatchSize*5) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	close(workChan)
	workerWg.Wait()
	close(resultChan)
	<-doneChan
	bktStart := time.Now()
	storage.FinalizeBulkLoad(newData)
	log.Printf("[SEARCH] BK-trees built in %v", time.Since(bktStart))
	s.index.SwapData(newData)
	indexedCount := atomic.LoadInt64(&indexed)
	log.Printf("[SEARCH] Index swap complete - all %d resources now searchable (total time: %v)", indexedCount, time.Since(startTime))
	if indexedCount >= int64(storage.MaxIndexDocuments) {
		log.Printf("[SEARCH] Memory index at capacity (%d docs). LRU eviction enabled, DB fallback active for searches.", storage.MaxIndexDocuments)
	}
	s.indexingMu.Lock()
	for cluster, resourceMap := range clusterVersionMap {
		if s.indexedResources[cluster] == nil {
			s.indexedResources[cluster] = make(map[string]time.Time)
		}
		for resource, indexTime := range resourceMap {
			s.indexedResources[cluster][resource] = indexTime
		}
	}
	s.indexingMu.Unlock()
	elapsed := time.Since(startTime)
	log.Printf("[SEARCH] Async load complete: loaded %d resources (%d kinds) from persisted index in %v",
		atomic.LoadInt64(&indexed), atomic.LoadInt64(&kindDefCount), elapsed)
}

func (s *Service) updateIndexingStatus(cluster string, completedAt *time.Time, err error) {
	s.indexingMu.Lock()
	defer s.indexingMu.Unlock()
	if status, ok := s.indexingStatus[cluster]; ok {
		if completedAt != nil {
			status.CompletedAt = completedAt
			status.IsComplete = true
		}
		if err != nil {
			status.LastError = err
		}
	}
}

func (s *Service) convertDBToSearchable(dbResource db.SearchableResource) storage.SearchableResource {
	labels := make(map[string]string)
	annotations := make(map[string]string)
	return storage.SearchableResource{
		ID:          dbResource.ResourceID,
		Cluster:     dbResource.Cluster,
		Kind:        dbResource.Kind,
		APIVersion:  dbResource.APIVersion,
		Name:        dbResource.Name,
		Namespace:   dbResource.Namespace,
		Description: dbResource.Description,
		Category:    dbResource.Category,
		Group:       dbResource.ResourceGroup,
		Version:     dbResource.ResourceVersion,
		Labels:      labels,
		Annotations: annotations,
		Keywords:    strings.Fields(dbResource.Keywords),
		CreatedAt:   dbResource.CreatedAt,
		UpdatedAt:   dbResource.UpdatedAt,
	}
}

func (s *Service) performSmartIndexing(cluster string) {
	log.Printf("[SEARCH] Performing smart indexing for cluster: %s", cluster)
	s.indexingMu.Lock()
	status := &IndexingStatus{
		StartedAt:        time.Now(),
		TotalResources:   0,
		IndexedResources: 0,
		FailedResources:  0,
		IsComplete:       false,
	}
	s.indexingStatus[cluster] = status
	if s.indexedResources[cluster] == nil {
		s.indexedResources[cluster] = make(map[string]time.Time)
	}
	s.indexingMu.Unlock()
	resources, err := s.k8sClient.ListAPIResources(cluster)
	if err != nil {
		log.Printf("Failed to list API resources for cluster %s: %v", cluster, err)
		s.updateIndexingStatus(cluster, nil, err)
		return
	}
	type res struct{ group, version, resource, kind string }
	toCheck := make([]res, 0)
	dynamicResources := map[string]bool{
		"pods": true, "deployments": true, "replicasets": true, "jobs": true,
		"configmaps": true, "secrets": true, "services": true,
		"endpoints": true, "endpointslices": true,
	}
	var priorityResources []res
	var otherResources []res
	for _, r := range resources {
		if strings.Contains(r.Name, "/") || !hasVerb(r.Verbs, "list") {
			continue
		}
		if r.Kind == "Event" || r.Name == "events" {
			continue
		}
		resourceInfo := res{group: r.Group, version: r.Version, resource: r.Name, kind: r.Kind}
		resourceKey := fmt.Sprintf("%s/%s/%s", r.Group, r.Version, r.Name)
		s.indexingMu.Lock()
		lastIndexed, exists := s.indexedResources[cluster][resourceKey]
		s.indexingMu.Unlock()
		if dynamicResources[r.Name] {
			if !exists || time.Since(lastIndexed) > 30*time.Second {
				priorityResources = append(priorityResources, resourceInfo)
			}
		} else if !exists || time.Since(lastIndexed) > 5*time.Minute {
			otherResources = append(otherResources, resourceInfo)
		}
	}
	toCheck = append(priorityResources, otherResources...)
	s.indexingMu.Lock()
	status.TotalResources = len(toCheck)
	s.indexingMu.Unlock()
	log.Printf("[SEARCH] Smart indexing %d priority + %d other resources for cluster %s",
		len(priorityResources), len(otherResources), cluster)
	const maxConcurrency = 1
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	indexOne := func(r res) {
		defer wg.Done()
		defer func() { <-sem }()
		select {
		case <-s.stopChan:
			return
		default:
		}
		time.Sleep(25 * time.Millisecond)
		err := s.smartIndexResourceType(cluster, r.group, r.version, r.resource)
		s.indexingMu.Lock()
		if err != nil {
			status.FailedResources++
			status.LastError = err
		} else {
			status.IndexedResources++
			resourceKey := fmt.Sprintf("%s/%s/%s", r.group, r.version, r.resource)
			s.indexedResources[cluster][resourceKey] = time.Now()
		}
		s.indexingMu.Unlock()
	}
	allResources := append(priorityResources, otherResources...)
	for _, r := range allResources {
		select {
		case <-s.stopChan:
			wg.Wait()
			return
		case sem <- struct{}{}:
			wg.Add(1)
			go indexOne(r)
		}
	}
	wg.Wait()
	now := time.Now()
	s.indexingMu.Lock()
	status.CompletedAt = &now
	status.IsComplete = true
	s.indexingMu.Unlock()
	log.Printf("[SEARCH] Smart indexing completed for cluster %s: %d/%d resources updated",
		cluster, status.IndexedResources, status.TotalResources)
}

func (s *Service) smartIndexResourceType(cluster, group, version, kind string) error {
	select {
	case <-s.stopChan:
		return context.Canceled
	default:
	}
	metadataClient, err := s.k8sClient.GetBulkMetadataClient(cluster)
	if err != nil {
		return fmt.Errorf("failed to get metadata client: %w", err)
	}
	resourceName := s.k8sClient.GetResourceName(cluster, group, version, kind)
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resourceName}
	resource := metadataClient.Resource(gvr)
	s.indexingMu.Lock()
	resourceKey := fmt.Sprintf("%s/%s/%s", group, version, kind)
	storedVersion := s.resourceVersions[cluster][resourceKey]
	s.indexingMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	listOpts := metav1.ListOptions{Limit: 1, ResourceVersion: "0"}
	checkList, err := resource.List(ctx, listOpts)
	cancel()
	if err != nil {
		return fmt.Errorf("failed to check %s/%s/%s: %w", group, version, kind, err)
	}
	newVersion := checkList.GetResourceVersion()
	if storedVersion != "" && storedVersion == newVersion {
		return nil
	}
	apiVersion := version
	if group != "" {
		apiVersion = group + "/" + version
	}
	idKind := utils.PluralizeKind(kind)
	category := getCategoryForKind(strings.ToLower(kind))
	var allSearchables []storage.SearchableResource
	continueToken := ""
	pageOpts := metav1.ListOptions{Limit: 500}
	for {
		select {
		case <-s.stopChan:
			return context.Canceled
		default:
		}
		if err := s.indexLimiter.Wait(context.Background()); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		pageOpts.Continue = continueToken
		list, err := resource.List(ctx, pageOpts)
		cancel()
		if err != nil {
			return fmt.Errorf("failed to list %s/%s/%s: %w", group, version, kind, err)
		}
		for i := range list.Items {
			item := &list.Items[i]
			name := item.Name
			namespace := item.Namespace
			var resID string
			if namespace != "" {
				resID = fmt.Sprintf("%s/%s/%s/%s/%s/%s", cluster, group, version, idKind, namespace, name)
			} else {
				resID = fmt.Sprintf("%s/%s/%s/%s/%s", cluster, group, version, idKind, name)
			}
			allSearchables = append(allSearchables, storage.SearchableResource{
				ID: resID, Cluster: cluster, Kind: kind, APIVersion: apiVersion,
				Name: name, Namespace: namespace, Category: category,
				Group: group, Version: version, Labels: item.Labels, Annotations: item.Annotations,
				UpdatedAt: time.Now(),
			})
		}
		continueToken = list.GetContinue()
		if continueToken == "" {
			break
		}
		runtime.Gosched()
	}
	if changed := s.eventHandler.filterChanged(allSearchables); len(changed) > 0 {
		numWorkers := runtime.GOMAXPROCS(0)
		if numWorkers > 4 {
			numWorkers = 4
		}
		prepared := make([]storage.PreparedResource, len(changed))
		chunkSize := (len(changed) + numWorkers - 1) / numWorkers
		var wg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			start := w * chunkSize
			end := start + chunkSize
			if end > len(changed) {
				end = len(changed)
			}
			if start >= end {
				break
			}
			wg.Add(1)
			go func(s, e int) {
				defer wg.Done()
				for i := s; i < e; i++ {
					prepared[i] = storage.PrepareResource(changed[i])
				}
			}(start, end)
		}
		wg.Wait()
		s.index.BatchIndexPrepared(prepared)
		if s.db != nil {
			for _, sr := range changed {
				s.eventHandler.persistResource(sr)
			}
		}
	}

	// Reconcile against the authoritative live set: drop any indexed document of
	// this type whose resource no longer exists in the cluster. This is what
	// cleans up entries left behind by missed/mismatched delete events.
	liveKeys := make(map[string]struct{}, len(allSearchables))
	for _, r := range allSearchables {
		key := r.Name
		if r.Namespace != "" {
			key = r.Namespace + "/" + r.Name
		}
		liveKeys[key] = struct{}{}
	}
	if removed := s.index.ReconcileType(cluster, group, version, liveKeys); len(removed) > 0 {
		log.Printf("[SEARCH] Reconcile removed %d stale %s/%s/%s entries in cluster %s", len(removed), group, version, kind, cluster)
		s.eventHandler.forgetFingerprints(removed)
		if s.db != nil {
			ids := removed
			go func() {
				for _, id := range ids {
					if err := s.db.DeleteSearchableResource(id); err != nil {
						log.Printf("[SEARCH] Failed to delete stale resource %s from DB: %v", id, err)
					}
				}
			}()
		}
	}

	s.indexingMu.Lock()
	if s.resourceVersions[cluster] == nil {
		s.resourceVersions[cluster] = make(map[string]string)
	}
	s.resourceVersions[cluster][resourceKey] = newVersion
	s.indexingMu.Unlock()
	return nil
}

func (s *Service) indexResourceKinds(cluster string) error {
	log.Printf("[SEARCH] Indexing resource kinds for cluster: %s", cluster)
	cacheKey := s.cache.BuildKey("api-resources", cluster)
	cached, err := s.cache.GetOrSet(cacheKey, 5*time.Minute, func() (interface{}, error) {
		return s.k8sClient.ListAPIResources(cluster)
	})
	if err != nil {
		return fmt.Errorf("failed to list API resources: %w", err)
	}
	resources := cached.([]metav1.APIResource)
	indexedKinds := 0
	for _, resource := range resources {
		if strings.Contains(resource.Name, "/") || !hasVerb(resource.Verbs, "list") {
			continue
		}
		if resource.Kind == "Event" || resource.Name == "events" {
			continue
		}
		kindDef := storage.SearchableResource{
			ID:          fmt.Sprintf("kind:%s:%s:%s:%s", cluster, resource.Group, resource.Version, resource.Kind),
			Cluster:     cluster,
			Kind:        "KindDefinition",
			Name:        resource.Kind,
			Namespace:   "",
			Category:    "Kind",
			APIVersion:  resource.Version,
			Group:       resource.Group,
			Version:     resource.Version,
			Description: fmt.Sprintf("%s resource kind", resource.Kind),
			Keywords:    []string{resource.Kind, strings.ToLower(resource.Kind), resource.Name},
			Labels: map[string]string{
				"resource-kind": resource.Kind,
				"resource-name": resource.Name,
				"namespaced":    fmt.Sprintf("%t", resource.Namespaced),
				"group":         resource.Group,
				"version":       resource.Version,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if category := getCategoryForKind(strings.ToLower(resource.Kind)); category != "Other" {
			kindDef.Category = category
		}
		if err := s.index.Index(kindDef); err != nil {
			log.Printf("[SEARCH] Failed to index kind %s: %v", resource.Kind, err)
		} else {
			indexedKinds++
		}
		if s.db != nil {
			s.eventHandler.persistResource(kindDef)
		}
	}
	log.Printf("[SEARCH] Indexed %d resource kinds for cluster %s", indexedKinds, cluster)
	s.invalidateSearchCacheForCluster(cluster)
	return nil
}

func (s *Service) schedulePeriodicReindexForActiveClusters() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.mu.RLock()
			var activeClusters []string
			cutoff := time.Now().Add(-30 * time.Minute)
			for cluster, lastAccess := range s.activeClusters {
				if lastAccess.After(cutoff) {
					activeClusters = append(activeClusters, cluster)
				}
			}
			s.mu.RUnlock()
			if len(activeClusters) == 0 {
				continue
			}
			log.Printf("[SEARCH] Periodic reindexing for %d active clusters (throttled)", len(activeClusters))
			for _, cluster := range activeClusters {
				s.indexingMu.Lock()
				if s.activeIndexing[cluster] {
					s.indexingMu.Unlock()
					continue
				}
				s.activeIndexing[cluster] = true
				s.indexingMu.Unlock()
				go func(c string) {
					defer func() {
						s.indexingMu.Lock()
						delete(s.activeIndexing, c)
						s.indexingMu.Unlock()
					}()
					if err := s.indexResourceKinds(c); err != nil {
						log.Printf("[SEARCH] Failed to refresh resource kinds for cluster %s: %v", c, err)
					}
					s.performSmartIndexing(c)
				}(cluster)
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
}
