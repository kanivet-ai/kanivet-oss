package search

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/faults"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/search/storage"
	"github.com/kanivet/backend/internal/utils"
	"golang.org/x/time/rate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Config struct {
	MaxConcurrentIndexers int
	IndexRateLimit        int
	SearchCacheTTL        int
	MaxRecentSearches     int
}

type RecentResource struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Cluster    string `json:"cluster"`
	APIVersion string `json:"apiVersion,omitempty"`
	Category   string `json:"category,omitempty"`
}

type IndexingStatus struct {
	StartedAt        time.Time
	CompletedAt      *time.Time
	TotalResources   int
	IndexedResources int
	FailedResources  int
	IsComplete       bool
	LastError        error
}

type ResourceEvent struct {
	Cluster   string
	Group     string
	Version   string
	Kind      string
	Namespace string
	Action    string
	Resource  map[string]interface{}
}

func defaultConfig() *Config {
	return &Config{
		MaxConcurrentIndexers: 2,
		IndexRateLimit:        10,
		SearchCacheTTL:        300,
		MaxRecentSearches:     20,
	}
}

type Service struct {
	k8sClient            *k8s.Client
	cache                *cache.Cache
	invalidationBus      *cache.InvalidationBus
	config               *Config
	db                   *db.DB
	index                *storage.ShardedIndex
	eventHandler         *ResourceEventHandler
	pendingMu            sync.Mutex
	pending              map[string]ResourceEvent
	pendingSeq           uint64
	wake                 chan struct{}
	isWatched            func(cluster, group, version, resource string) bool
	stopChan             chan struct{}
	wg                   sync.WaitGroup
	mu                   sync.RWMutex
	recentSearches       []RecentResource
	watchedTopics        map[string]bool
	activeClusters       map[string]time.Time
	clusterSearchVersion map[string]uint64
	indexingMu           sync.Mutex
	activeIndexing       map[string]bool
	indexedResources     map[string]map[string]time.Time
	indexingStatus       map[string]*IndexingStatus
	resourceVersions     map[string]map[string]string
	indexLimiter         *rate.Limiter
	onIndexingComplete   func(cluster string)
	now                  func() time.Time
	versionLastBump      map[string]time.Time
	versionBumpPending   map[string]bool
}

func (s *Service) nowFn() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) RemoveCluster(cluster string) int {
	removed := s.index.RemoveCluster(cluster)
	s.mu.Lock()
	delete(s.activeClusters, cluster)
	delete(s.clusterSearchVersion, cluster)
	for t := range s.watchedTopics {
		if strings.HasPrefix(t, "items:"+cluster+":") {
			delete(s.watchedTopics, t)
		}
	}
	s.mu.Unlock()
	s.indexingMu.Lock()
	delete(s.indexedResources, cluster)
	delete(s.indexingStatus, cluster)
	delete(s.resourceVersions, cluster)
	s.indexingMu.Unlock()
	if s.eventHandler != nil {
		s.eventHandler.forgetCluster(cluster)
	}
	if s.db != nil {
		if err := s.db.DeleteSearchableResourcesByCluster(cluster); err != nil {
			log.Printf("[SEARCH] Failed to purge DB rows for cluster %s: %v", cluster, err)
		}
	}
	s.invalidateSearchCacheForCluster("")
	log.Printf("[SEARCH] Removed cluster %s from index (%d docs)", cluster, removed)
	return removed
}

func NewService(k8sClient *k8s.Client, cache *cache.Cache, database *db.DB, invalidationBus *cache.InvalidationBus) *Service {
	index := storage.NewShardedIndex(storage.DefaultSearchShards)
	batchWriter := NewBatchWriter(database)
	s := &Service{
		k8sClient:            k8sClient,
		cache:                cache,
		invalidationBus:      invalidationBus,
		config:               defaultConfig(),
		db:                   database,
		index:                index,
		recentSearches:       make([]RecentResource, 0, 20),
		eventHandler:         &ResourceEventHandler{index: index, db: database, batchWriter: batchWriter},
		pending:              make(map[string]ResourceEvent, 64),
		wake:                 make(chan struct{}, 1),
		stopChan:             make(chan struct{}),
		watchedTopics:        make(map[string]bool),
		activeClusters:       make(map[string]time.Time),
		clusterSearchVersion: make(map[string]uint64),
		activeIndexing:       make(map[string]bool),
		indexedResources:     make(map[string]map[string]time.Time),
		indexingStatus:       make(map[string]*IndexingStatus),
		resourceVersions:     make(map[string]map[string]string),
		indexLimiter:         rate.NewLimiter(rate.Limit(100), 25),
	}
	index.SetEvictionCallback(func(evicted []storage.SearchableResource) {})
	if home, err := os.UserHomeDir(); err == nil {
		if err := index.SetupWarmTiers(filepath.Join(home, ".kanivet", "search")); err != nil {
			log.Printf("search: warm tier init failed: %v", err)
		}
	}
	if invalidationBus != nil {
		invalidationBus.Subscribe(s)
	}
	s.wg.Add(1)
	go s.processEvents()
	go s.loadPersistedIndexAsync()
	go s.schedulePeriodicReindexForActiveClusters()
	go s.runCompactionLoop()
	return s
}

func (s *Service) runCompactionLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.index.MaybeCompactAll()
		}
	}
}

// SetWatchedChecker lets the periodic re-indexer skip resource types that a
// live watch already keeps fresh.
func (s *Service) SetWatchedChecker(fn func(cluster, group, version, resource string) bool) {
	s.isWatched = fn
}

// OnResourceEvent coalesces events per resource identity instead of queueing
// them: a burst of MODIFIED events for one pod costs a single index pass, and
// nothing is dropped under load.
func (s *Service) OnResourceEvent(cluster, group, version, kind, namespace, action string, resource map[string]interface{}) {
	name, _ := resource["name"].(string)
	ns, _ := resource["namespace"].(string)
	if ns == "" {
		ns = namespace
	}
	key := cluster + "|" + group + "|" + version + "|" + kind + "|" + ns + "|" + name
	s.pendingMu.Lock()
	if name == "" {
		s.pendingSeq++
		key = fmt.Sprintf("%s|#%d", key, s.pendingSeq)
	}
	s.pending[key] = ResourceEvent{Cluster: cluster, Group: group, Version: version, Kind: kind, Namespace: namespace, Action: action, Resource: resource}
	s.pendingMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) drainEvents() []ResourceEvent {
	s.pendingMu.Lock()
	pending := s.pending
	s.pending = make(map[string]ResourceEvent, 64)
	s.pendingMu.Unlock()
	events := make([]ResourceEvent, 0, len(pending))
	for _, e := range pending {
		events = append(events, e)
	}
	return events
}

func (s *Service) handleEventSafe(evt ResourceEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] handleEvent for %s/%s: %v", evt.Kind, evt.Namespace, r)
			faults.CaptureExceptionWithContext(
				fmt.Errorf("panic in handleEvent: %v", r),
				map[string]any{"kind": evt.Kind, "namespace": evt.Namespace, "panic": r, "stack": string(debug.Stack())},
			)
			panic(r) // Re-panic to crash
		}
	}()
	s.handleEvent(evt)
}

func (s *Service) processEvents() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] processEvents: %v", r)
			faults.CaptureExceptionWithContext(
				fmt.Errorf("panic in processEvents: %v", r),
				map[string]any{"panic": r, "stack": string(debug.Stack())},
			)
			panic(r) // Re-panic to crash
		}
	}()
	const workers = 2
	batchTimeout := 100 * time.Millisecond
	timer := time.NewTimer(batchTimeout)
	timer.Stop()
	armed := false
	process := func() {
		events := s.drainEvents()
		if len(events) == 0 {
			return
		}
		ch := make(chan ResourceEvent)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for evt := range ch {
					s.handleEventSafe(evt)
				}
			}()
		}
		for _, evt := range events {
			ch <- evt
		}
		close(ch)
		wg.Wait()
	}
	for {
		select {
		case <-s.stopChan:
			process()
			return
		case <-s.wake:
			if !armed {
				timer.Reset(batchTimeout)
				armed = true
			}
		case <-timer.C:
			armed = false
			process()
		}
	}
}

func (s *Service) handleEvent(event ResourceEvent) {
	if nk := utils.PluralizeKind(event.Kind); (nk == "customresourcedefinitions" || nk == "apiservices") &&
		(event.Action == "ADDED" || event.Action == "added" || event.Action == "DELETED" || event.Action == "deleted") {
		log.Printf("[SEARCH] Detected %s %s in cluster %s", event.Kind, event.Action, event.Cluster)
		cacheKey := s.cache.BuildKey("api-resources", event.Cluster)
		s.cache.Delete(cacheKey)
		if event.Action == "ADDED" || event.Action == "added" {
			go func() {
				time.Sleep(2 * time.Second)
				if err := s.indexResourceKinds(event.Cluster); err != nil {
					log.Printf("[SEARCH] Failed to re-index kinds after %s %s: %v", event.Kind, event.Action, err)
				}
			}()
		}
	}
	if event.Namespace != "" {
		if _, ok := event.Resource["namespace"]; !ok {
			event.Resource["namespace"] = event.Namespace
		}
	}
	switch event.Action {
	case "ADDED", "MODIFIED", "added", "modified":
		changed, err := s.eventHandler.OnAdd(event.Cluster, event.Resource)
		if err != nil {
			log.Printf("Failed to index resource on %s: %v", event.Action, err)
		}
		if changed {
			s.invalidateSearchCacheForCluster(event.Cluster)
		}
	case "DELETED", "deleted":
		if err := s.eventHandler.onDeleteWithCoords(event.Cluster, event.Group, event.Version, event.Kind, event.Resource); err != nil {
			log.Printf("Failed to remove resource on DELETE: %v", err)
		}
		s.invalidateSearchCacheForCluster(event.Cluster)
	default:
		log.Printf("Unknown event action: %s", event.Action)
	}
}

const versionBumpInterval = 2 * time.Second

// bumpVersionLocked coalesces version bumps into at most one per interval per
// key; a suppressed bump is marked pending and applied lazily by
// searchVersions, so every change is reflected within one interval.
func (s *Service) bumpVersionLocked(key string, now time.Time) {
	if s.versionLastBump == nil {
		s.versionLastBump = make(map[string]time.Time)
		s.versionBumpPending = make(map[string]bool)
	}
	if now.Sub(s.versionLastBump[key]) >= versionBumpInterval {
		s.clusterSearchVersion[key]++
		s.versionLastBump[key] = now
		delete(s.versionBumpPending, key)
	} else {
		s.versionBumpPending[key] = true
	}
}

func (s *Service) invalidateSearchCacheForCluster(cluster string) {
	s.mu.Lock()
	if s.clusterSearchVersion == nil {
		s.clusterSearchVersion = make(map[string]uint64)
	}
	now := s.nowFn()
	s.bumpVersionLocked("", now)
	if cluster != "" {
		s.bumpVersionLocked(cluster, now)
	}
	s.mu.Unlock()
}

func (s *Service) searchVersions(cluster string) (uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowFn()
	for _, key := range []string{"", cluster} {
		if s.versionBumpPending[key] && now.Sub(s.versionLastBump[key]) >= versionBumpInterval {
			s.clusterSearchVersion[key]++
			s.versionLastBump[key] = now
			delete(s.versionBumpPending, key)
		}
	}
	return s.clusterSearchVersion[""], s.clusterSearchVersion[cluster]
}

func (s *Service) OnInvalidate(pattern string) {
	if strings.HasPrefix(pattern, "search") || strings.HasPrefix(pattern, "items:") {
		s.invalidateSearchCacheForCluster("")
	}
}

func (s *Service) Stop() {
	close(s.stopChan)
	s.wg.Wait()
	if s.eventHandler.batchWriter != nil {
		s.eventHandler.batchWriter.Stop()
	}
	s.mu.Lock()
	s.watchedTopics = make(map[string]bool)
	s.mu.Unlock()
}

func (s *Service) ListAPIResources(cluster string) ([]metav1.APIResource, error) {
	return s.k8sClient.ListAPIResources(cluster)
}
