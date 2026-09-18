package search

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/search/storage"
	"github.com/kanivet/backend/internal/utils"
	"gorm.io/gorm"
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
		s.ensureClusterLoaded(cluster)
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

// bootLoadBudget caps how many regular documents the persisted index load
// brings into memory at startup. Clusters are loaded most recently indexed
// first until the budget is spent; the rest stay searchable through the
// database and are loaded when their tab is opened. Kind definitions are
// always loaded since they drive kind autocomplete. Tests shrink it.
var bootLoadBudget = storage.MaxIndexDocuments

// maxConcurrentSweeps bounds how many clusters run a full LIST sweep at once.
const maxConcurrentSweeps = 2

const loadBatchSize = 2000

// forEachRowBatch streams rows matching query in id order, calling fn with
// each batch. query must return a fresh builder on every call so conditions
// do not accumulate between pages. It returns the number of rows seen.
func (s *Service) forEachRowBatch(query func() *gorm.DB, fn func([]db.SearchableResource)) int {
	var lastID uint
	total := 0
	for {
		var rows []db.SearchableResource
		if err := query().Where("id > ?", lastID).Order("id ASC").Limit(loadBatchSize).Find(&rows).Error; err != nil {
			log.Printf("[SEARCH] Failed to load rows after id %d: %v", lastID, err)
			return total
		}
		if len(rows) == 0 {
			return total
		}
		lastID = rows[len(rows)-1].ID
		total += len(rows)
		fn(rows)
		if len(rows) < loadBatchSize {
			return total
		}
	}
}

// preparePipeline tokenizes database rows on a couple of workers and hands
// the prepared documents, in arrival order, to a single consumer. The
// consumer owns the non-thread-safe loader.
type preparePipeline struct {
	in   chan []db.SearchableResource
	done chan struct{}
	wg   sync.WaitGroup
}

func (s *Service) startPreparePipeline(consume func([]storage.PreparedResource)) *preparePipeline {
	p := &preparePipeline{in: make(chan []db.SearchableResource, 4), done: make(chan struct{})}
	out := make(chan []storage.PreparedResource, 4)
	for i := 0; i < 2; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for rows := range p.in {
				prepared := make([]storage.PreparedResource, 0, len(rows))
				for _, row := range rows {
					prepared = append(prepared, storage.PrepareResource(s.convertDBToSearchable(row)))
				}
				out <- prepared
			}
		}()
	}
	go func() {
		p.wg.Wait()
		close(out)
	}()
	go func() {
		for prepared := range out {
			consume(prepared)
		}
		close(p.done)
	}()
	return p
}

func (p *preparePipeline) submit(rows []db.SearchableResource) { p.in <- rows }

func (p *preparePipeline) finish() {
	close(p.in)
	<-p.done
}

// loadPersistedIndexAsync rebuilds the in-memory index from the database in a
// single pass: every row is tokenized once and lands directly in its shard.
// Kind definitions for every cluster load first, then clusters in order of
// most recent indexing until bootLoadBudget regular documents are in memory.
// Whatever is left stays in the database, reachable through the DB fallback
// and loaded into memory when its cluster is opened.
func (s *Service) loadPersistedIndexAsync() {
	if s.db == nil {
		return
	}
	startTime := time.Now()
	var totalCount int64
	if err := s.db.Model(&db.SearchableResource{}).Count(&totalCount).Error; err != nil {
		log.Printf("[SEARCH] Failed to count persisted resources: %v", err)
		return
	}
	if totalCount == 0 {
		log.Printf("[SEARCH] No resources to load from database")
		return
	}
	log.Printf("[SEARCH] Loading persisted search index: %d rows in database, budget %d documents", totalCount, bootLoadBudget)

	// Only the ordering matters here, so the MAX() text SQLite returns is not
	// parsed; rows written by one driver sort correctly as strings.
	var order []struct {
		Cluster string
		Newest  string
	}
	if err := s.db.Raw(`SELECT cluster, MAX(indexed_at) AS newest FROM searchable_resources
		WHERE kind <> 'KindDefinition' GROUP BY cluster ORDER BY newest DESC`).Scan(&order).Error; err != nil {
		log.Printf("[SEARCH] Failed to rank clusters by recency: %v", err)
		return
	}

	capHint := int(min(totalCount, int64(bootLoadBudget)+int64(bootLoadBudget)/4))
	loader := s.index.NewBulkLoader(capHint)
	clusterVersionMap := make(map[string]map[string]time.Time)
	var kindDefCount int64
	pipeline := s.startPreparePipeline(func(prepared []storage.PreparedResource) {
		loader.Add(prepared)
		for _, p := range prepared {
			r := p.Resource
			if r.Kind == "KindDefinition" {
				kindDefCount++
				continue
			}
			if clusterVersionMap[r.Cluster] == nil {
				clusterVersionMap[r.Cluster] = make(map[string]time.Time)
			}
			resourceKey := fmt.Sprintf("%s/%s/%s", r.Group, r.Version, utils.PluralizeKind(r.Kind))
			if r.UpdatedAt.After(clusterVersionMap[r.Cluster][resourceKey]) {
				clusterVersionMap[r.Cluster][resourceKey] = r.UpdatedAt
			}
		}
	})

	s.forEachRowBatch(func() *gorm.DB { return s.db.Where("kind = ?", "KindDefinition") }, pipeline.submit)

	loaded := make([]string, 0, len(order))
	docs := 0
	skipped := 0
	lastLog := time.Now()
	for _, c := range order {
		if docs >= bootLoadBudget {
			skipped++
			continue
		}
		cluster := c.Cluster
		docs += s.forEachRowBatch(func() *gorm.DB {
			return s.db.Where("cluster = ? AND kind <> ?", cluster, "KindDefinition")
		}, pipeline.submit)
		loaded = append(loaded, cluster)
		if time.Since(lastLog) > 2*time.Second {
			log.Printf("[SEARCH] Loading index: %d documents from %d clusters so far", docs, len(loaded))
			lastLog = time.Now()
		}
	}
	pipeline.finish()

	finalizeStart := time.Now()
	loader.Finish()
	log.Printf("[SEARCH] Shards finalized in %v", time.Since(finalizeStart).Round(time.Millisecond))

	s.loadedMu.Lock()
	if s.loadedClusters == nil {
		s.loadedClusters = make(map[string]bool)
	}
	for _, c := range loaded {
		s.loadedClusters[c] = true
	}
	s.loadedMu.Unlock()
	s.dbHasUnloaded.Store(skipped > 0)

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

	log.Printf("[SEARCH] Persisted index loaded: %d documents and %d kind definitions from %d clusters in %v (%d clusters left in the database, loaded on open)",
		docs, kindDefCount, len(loaded), time.Since(startTime).Round(time.Millisecond), skipped)
}

// ensureClusterLoaded pulls a cluster's persisted documents into memory if the
// boot load left them in the database. It is called when the cluster's tab
// opens, before the LIST sweep, so search over that cluster is complete while
// the sweep refreshes it.
func (s *Service) ensureClusterLoaded(cluster string) {
	if s.db == nil {
		return
	}
	s.loadedMu.Lock()
	if s.loadedClusters == nil {
		s.loadedClusters = make(map[string]bool)
	}
	already := s.loadedClusters[cluster]
	if !already {
		s.loadedClusters[cluster] = true
	}
	s.loadedMu.Unlock()
	if already {
		return
	}
	start := time.Now()
	n := s.forEachRowBatch(func() *gorm.DB {
		return s.db.Where("cluster = ? AND kind <> ?", cluster, "KindDefinition")
	}, func(rows []db.SearchableResource) {
		prepared := make([]storage.PreparedResource, 0, len(rows))
		for _, row := range rows {
			prepared = append(prepared, storage.PrepareResource(s.convertDBToSearchable(row)))
		}
		s.index.BatchIndexPrepared(prepared)
	})
	if n > 0 {
		log.Printf("[SEARCH] Loaded %d persisted documents for cluster %s on open in %v", n, cluster, time.Since(start).Round(time.Millisecond))
	}
}

// acquireSweepSlot blocks until this cluster may run a LIST sweep, or the
// service stops. It returns a release func, or nil if stopping.
func (s *Service) acquireSweepSlot() func() {
	if s.sweepSem == nil {
		return func() {}
	}
	select {
	case s.sweepSem <- struct{}{}:
		return func() { <-s.sweepSem }
	case <-s.stopChan:
		return nil
	}
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
	release := s.acquireSweepSlot()
	if release == nil {
		return
	}
	defer release()
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
	resources, err := s.apiResources(cluster)
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
		// A live watch already streams this type into the index; re-listing it
		// would only repeat work the watcher has done.
		if s.isWatched != nil && s.isWatched(cluster, r.Group, r.Version, r.Name) {
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
	resourceKey := fmt.Sprintf("%s/%s/%s", group, version, kind)
	var newVersion string
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
		if newVersion == "" {
			newVersion = list.GetResourceVersion()
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
	if removed := s.index.ReconcileType(cluster, group, version, kind, liveKeys); len(removed) > 0 {
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

func (s *Service) apiResources(cluster string) ([]metav1.APIResource, error) {
	cached, err := s.cache.GetOrSet(s.cache.BuildKey("api-resources", cluster), 5*time.Minute, func() (interface{}, error) {
		return s.k8sClient.ListAPIResources(cluster)
	})
	if err != nil {
		return nil, err
	}
	return cached.([]metav1.APIResource), nil
}

func (s *Service) indexResourceKinds(cluster string) error {
	log.Printf("[SEARCH] Indexing resource kinds for cluster: %s", cluster)
	resources, err := s.apiResources(cluster)
	if err != nil {
		return fmt.Errorf("failed to list API resources: %w", err)
	}
	kindDefs := make([]storage.SearchableResource, 0, len(resources))
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
		kindDefs = append(kindDefs, kindDef)
	}
	changed := s.eventHandler.filterChanged(kindDefs)
	indexedKinds := 0
	for _, kindDef := range changed {
		if err := s.index.Index(kindDef); err != nil {
			log.Printf("[SEARCH] Failed to index kind %s: %v", kindDef.Name, err)
		} else {
			indexedKinds++
		}
		if s.db != nil {
			s.eventHandler.persistResource(kindDef)
		}
	}
	log.Printf("[SEARCH] Indexed %d/%d resource kinds for cluster %s", indexedKinds, len(kindDefs), cluster)
	if len(changed) > 0 {
		s.invalidateSearchCacheForCluster(cluster)
	}
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
