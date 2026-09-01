package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/faults"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/watcher/listadapters"
	"github.com/kanivet/backend/internal/topics"
	"github.com/kanivet/backend/internal/websocket/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// CountUpdateMessage represents a resource count update for the tree sidebar
type CountUpdateMessage struct {
	core.BaseMessage `json:",inline"`
	Channel          string `json:"channel"`
	Topic            string `json:"topic"`
	Group            string `json:"group"`
	Resource         string `json:"resource"`
	Count            int    `json:"count"`
}

func (m *CountUpdateMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func (m *CountUpdateMessage) GetData() map[string]interface{} {
	return map[string]interface{}{
		"type":     m.MessageType,
		"channel":  m.Channel,
		"topic":    m.Topic,
		"group":    m.Group,
		"resource": m.Resource,
		"count":    m.Count,
	}
}

type syncStatus struct {
	done chan struct{}
	err  error
}

type Service struct {
	client            *k8s.Client
	hub               Broadcaster
	manager           *WatchManager
	cache             *ResourceCache
	invalidationBus   *cache.InvalidationBus
	db                *db.DB
	onClusterWatched  func(cluster string)
	indexedClustersMu sync.Mutex
	indexedClusters   map[string]bool
	countThrottler    *countThrottler
	invalThrottler    *invalidationThrottler
	syncState         map[string]*syncStatus
	syncStateMu       sync.RWMutex
	clusterErrMu      sync.Mutex
	clusterErr        map[string]*clusterErrState
	epochMu           sync.Mutex
	epochs            map[string]uint64
}

func (s *Service) nextEpoch(topic string) uint64 {
	s.epochMu.Lock()
	defer s.epochMu.Unlock()
	s.epochs[topic]++
	return s.epochs[topic]
}

func (s *Service) clearEpoch(topic string) {
	s.epochMu.Lock()
	delete(s.epochs, topic)
	s.epochMu.Unlock()
}

type clusterErrState struct {
	failures int
	firstAt  time.Time
	lastCode string
	sent     bool
}

type countThrottler struct {
	mu          sync.Mutex
	pending     map[string]map[string]int // cluster -> "group/resource" -> count
	timer       *time.Timer
	interval    time.Duration
	publishFunc func(cluster, group, resource string, count int)
}

func newCountThrottler(interval time.Duration, publishFunc func(cluster, group, resource string, count int)) *countThrottler {
	return &countThrottler{
		pending:     make(map[string]map[string]int),
		interval:    interval,
		publishFunc: publishFunc,
	}
}

func (ct *countThrottler) update(cluster, group, resource string, count int) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if ct.pending[cluster] == nil {
		ct.pending[cluster] = make(map[string]int)
	}
	ct.pending[cluster][group+"/"+resource] = count
	if ct.timer == nil {
		ct.timer = time.AfterFunc(ct.interval, ct.send)
	}
}

func (ct *countThrottler) send() {
	ct.mu.Lock()
	pending := ct.pending
	ct.pending = make(map[string]map[string]int)
	ct.timer = nil
	ct.mu.Unlock()
	for cluster, resources := range pending {
		for key, count := range resources {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) == 2 {
				ct.publishFunc(cluster, parts[0], parts[1], count)
			}
		}
	}
}

func (ct *countThrottler) shutdown() {
	ct.mu.Lock()
	if ct.timer != nil {
		ct.timer.Stop()
		ct.timer = nil
	}
	ct.mu.Unlock()
}

type invalidationThrottler struct {
	mu        sync.Mutex
	pending   map[string]struct{}
	timer     *time.Timer
	interval  time.Duration
	flushFunc func(patterns []string)
}

func newInvalidationThrottler(interval time.Duration, flushFunc func(patterns []string)) *invalidationThrottler {
	return &invalidationThrottler{
		pending:   make(map[string]struct{}),
		interval:  interval,
		flushFunc: flushFunc,
	}
}

func (it *invalidationThrottler) add(patterns ...string) {
	it.mu.Lock()
	for _, p := range patterns {
		it.pending[p] = struct{}{}
	}
	if it.timer == nil {
		it.timer = time.AfterFunc(it.interval, it.send)
	}
	it.mu.Unlock()
}

func (it *invalidationThrottler) send() {
	it.mu.Lock()
	pending := it.pending
	it.pending = make(map[string]struct{})
	it.timer = nil
	it.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	patterns := make([]string, 0, len(pending))
	for p := range pending {
		patterns = append(patterns, p)
	}
	it.flushFunc(patterns)
}

func (it *invalidationThrottler) shutdown() {
	it.mu.Lock()
	if it.timer != nil {
		it.timer.Stop()
		it.timer = nil
	}
	it.mu.Unlock()
}

func (s *Service) SetOnClusterWatched(cb func(cluster string)) {
	s.onClusterWatched = cb
}

type Broadcaster interface {
	Broadcast(topic string, message Message) error
	BroadcastDirect(topic string, message Message) error
	BroadcastAll(message Message) error
	FlushTopic(topic string) error
	SetSortPreference(topic, sortBy, sortOrder string)
	GetSortPreference(topic string) (sortBy, sortOrder string)
	CleanupTopic(topic string)
}

type Message interface {
	Marshal() ([]byte, error)
	GetData() map[string]interface{}
}

func NewService(client *k8s.Client, hub Broadcaster) *Service {
	manager := NewWatchManager()
	manager.SetMaxWatches(defaultMaxWatches)
	resourceCache := NewResourceCache()
	svc := &Service{
		client:          client,
		hub:             hub,
		manager:         manager,
		cache:           resourceCache,
		indexedClusters: make(map[string]bool),
		syncState:       make(map[string]*syncStatus),
		clusterErr:      make(map[string]*clusterErrState),
		epochs:          make(map[string]uint64),
	}
	manager.SetOnCleanup(func(key string) {
		cleaned := manager.CleanupIfNotWatching(key, func() {
			resourceCache.Clear(key)
			hub.CleanupTopic(key)
			svc.clearSyncState(key)
			svc.clearEpoch(key)
		})
		if cleaned {
			log.Printf("k8s watcher: cleared cache and batcher state for %s", key)
		} else {
			log.Printf("k8s watcher: skipping cleanup for %s (watch recreated)", key)
		}
	})
	svc.countThrottler = newCountThrottler(500*time.Millisecond, svc.doPublishCount)
	svc.invalThrottler = newInvalidationThrottler(500*time.Millisecond, func(patterns []string) {
		if svc.invalidationBus == nil {
			return
		}
		for _, p := range patterns {
			svc.invalidationBus.InvalidatePattern(p)
		}
	})
	go svc.relayVClusterStatus()
	return svc
}

func (s *Service) relayVClusterStatus() {
	_, ch := s.client.SubscribeVClusterStatus()
	for status := range ch {
		msg := &VClusterStatusMessage{
			BaseMessage: core.BaseMessage{
				MessageType: "vcluster_status",
				Timestamp:   time.Now(),
			},
			Cluster:    status.ID,
			State:      string(status.State),
			Generation: status.Generation,
			LocalPort:  status.LocalPort,
			Detail:     status.Detail,
		}
		if err := s.hub.BroadcastAll(msg); err != nil {
			log.Printf("Failed to broadcast vcluster_status for %s: %v", status.ID, err)
		}
	}
}

type VClusterStatusMessage struct {
	core.BaseMessage `json:",inline"`
	Cluster          string `json:"cluster"`
	State            string `json:"state"`
	Generation       uint64 `json:"generation"`
	LocalPort        int    `json:"localPort,omitempty"`
	Detail           string `json:"detail,omitempty"`
}

func (m *VClusterStatusMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func (m *VClusterStatusMessage) GetData() map[string]any {
	return map[string]any{
		"type":       m.MessageType,
		"cluster":    m.Cluster,
		"state":      m.State,
		"generation": m.Generation,
		"localPort":  m.LocalPort,
		"detail":     m.Detail,
	}
}

func (s *Service) GetCache() *ResourceCache {
	return s.cache
}

func (s *Service) clearSyncState(topic string) {
	s.syncStateMu.Lock()
	delete(s.syncState, topic)
	s.syncStateMu.Unlock()
}

func (s *Service) SetInvalidationBus(bus *cache.InvalidationBus) {
	s.invalidationBus = bus
}

func (s *Service) SetDB(database *db.DB) {
	s.db = database
}

func (s *Service) Shutdown() {
	if s.countThrottler != nil {
		s.countThrottler.shutdown()
	}
	if s.invalThrottler != nil {
		s.invalThrottler.shutdown()
	}
	s.manager.Shutdown()
	for _, topic := range s.cache.Topics() {
		s.saveSnapshot(topic)
	}
}

func (s *Service) StartWatch(cluster, group, version, kind, namespace, sortBy, sortOrder string) error {
	t0 := time.Now()
	topic := topics.BuildItemsTopic(cluster, group, version, kind, namespace)
	if sortBy != "" || sortOrder != "" {
		s.hub.SetSortPreference(topic, sortBy, sortOrder)
	}
	s.indexedClustersMu.Lock()
	needsIndexing := !s.indexedClusters[cluster]
	if needsIndexing {
		s.indexedClusters[cluster] = true
	}
	s.indexedClustersMu.Unlock()
	if needsIndexing && s.onClusterWatched != nil {
		go s.onClusterWatched(cluster)
	}
	resourceName := s.client.GetResourceName(cluster, group, version, kind)
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resourceName}
	t1 := time.Now()
	alreadyWatching, err := s.manager.StartWatch(topic, func(ctx context.Context) error {
		return s.watchResources(ctx, cluster, gvr, namespace, topic)
	})
	t2 := time.Now()
	if alreadyWatching {
		go s.sendCachedData(topic, sortBy, sortOrder)
		log.Printf("[PERF] StartWatch (cached): setup=%v managerStart=%v kind=%s", t1.Sub(t0), t2.Sub(t1), kind)
	} else {
		log.Printf("[PERF] StartWatch (fresh): setup=%v managerStart=%v kind=%s", t1.Sub(t0), t2.Sub(t1), kind)
	}
	return err
}

func (s *Service) StopWatch(cluster, group, version, kind, namespace string) {
	topic := topics.BuildItemsTopic(cluster, group, version, kind, namespace)
	s.manager.StopWatch(topic)
}

// StopAllForCluster immediately reaps the cluster's idle watches (refcount 0,
// normally waiting out the grace period) and clears their per-topic state.
// Watches with live subscribers survive: a tab close fires this over HTTP and
// can race a quick reopen whose fresh subscriptions must not be killed.
func (s *Service) StopAllForCluster(cluster string) {
	prefix := "items:" + cluster + ":"
	stopped := s.manager.StopAllPrefix(prefix)
	for _, topic := range stopped {
		s.cache.Clear(topic)
		s.hub.CleanupTopic(topic)
		s.clearSyncState(topic)
		s.clearEpoch(topic)
	}
	s.indexedClustersMu.Lock()
	delete(s.indexedClusters, cluster)
	s.indexedClustersMu.Unlock()
	s.clusterErrMu.Lock()
	delete(s.clusterErr, cluster)
	s.clusterErrMu.Unlock()
	if len(stopped) > 0 {
		log.Printf("k8s watcher: force-stopped %d watches for cluster %s", len(stopped), cluster)
	}
}

// Resync re-pushes the cached snapshot for a topic to its subscribers. It is
// used to recover a client that missed a live update due to backpressure,
// instead of letting it silently desync. The topic's stored sort preference is
// honored so the resynced list matches the client's view.
func (s *Service) Resync(topic string) {
	sortBy, sortOrder := s.hub.GetSortPreference(topic)
	s.sendCachedData(topic, sortBy, sortOrder)
}

func (s *Service) sendCachedData(topic, sortBy, sortOrder string) {
	t0 := time.Now()
	s.syncStateMu.RLock()
	status := s.syncState[topic]
	s.syncStateMu.RUnlock()
	if status != nil {
		select {
		case <-status.done:
			if status.err != nil {
				return
			}
		case <-time.After(30 * time.Second):
			return
		}
	}
	if !s.manager.HasWatch(topic) {
		return
	}
	if err := s.hub.FlushTopic(topic); err != nil {
		log.Printf("Failed to flush topic %s before resync: %v", topic, err)
	}
	cachedItems := s.cache.GetAll(topic)
	epoch := s.nextEpoch(topic)
	sortCachedItems(cachedItems, sortBy, sortOrder)
	for i := 0; i < len(cachedItems); {
		size := 200
		if i == 0 {
			size = 100
		}
		end := i + size
		if end > len(cachedItems) {
			end = len(cachedItems)
		}
		chunk := cachedItems[i:end]
		msg := &BulkListMessage{
			BaseMessage: core.BaseMessage{MessageType: "bulk_list", Timestamp: time.Now()},
			Channel:     "items",
			Topic:       topic,
			Items:       chunk,
			Count:       len(chunk),
			Epoch:       epoch,
		}
		if err := s.hub.BroadcastDirect(topic, msg); err != nil {
			log.Printf("Failed to broadcast cached data chunk for topic %s: %v", topic, err)
		}
		i = end
	}
	s.sendInitialSyncComplete(topic, len(cachedItems), epoch)
	log.Printf("[PERF] sendCachedData: %d items in %v (epoch=%d)", len(cachedItems), time.Since(t0), epoch)
}

func sortCachedItems(items []map[string]interface{}, sortBy, sortOrder string) {
	if len(items) == 0 {
		return
	}
	isDesc := sortOrder == "desc"
	sort.Slice(items, func(i, j int) bool {
		var vi, vj interface{}
		if sortBy == "age" || sortBy == "creationTimestamp" {
			vi = items[i]["creationTimestamp"]
			vj = items[j]["creationTimestamp"]
		} else {
			vi = items[i][sortBy]
			vj = items[j][sortBy]
		}
		cmp := compareValues(vi, vj)
		if isDesc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func compareValues(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch va := a.(type) {
	case string:
		if vb, ok := b.(string); ok {
			return strings.Compare(strings.ToLower(va), strings.ToLower(vb))
		}
	case int:
		if vb, ok := b.(int); ok {
			if va < vb {
				return -1
			}
			if va > vb {
				return 1
			}
			return 0
		}
	case int64:
		if vb, ok := b.(int64); ok {
			if va < vb {
				return -1
			}
			if va > vb {
				return 1
			}
			return 0
		}
	case float64:
		if vb, ok := b.(float64); ok {
			if va < vb {
				return -1
			}
			if va > vb {
				return 1
			}
			return 0
		}
	}
	sa := strings.ToLower(stringValue(a))
	sb := strings.ToLower(stringValue(b))
	return strings.Compare(sa, sb)
}

func stringValue(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

const maxSnapshotItems = 2500

// snapshotEligible reports whether a topic's items should be persisted for
// stale-then-fresh cold starts. Events already have a DB-backed instant path.
func snapshotEligible(topic string) (cluster string, ok bool) {
	cluster, group, _, kind, _, parsed := topics.ParseItemsTopic(topic)
	if !parsed || (group == "" && kind == "events") {
		return "", false
	}
	return cluster, true
}

// preloadSnapshot broadcasts the last persisted list for a topic before the
// live LIST runs, so cold opens render instantly. Items go into the resource
// cache so the post-LIST stale diff emits deletions for anything that vanished
// while the app was closed. No initial-sync-complete is sent here: the live
// LIST that follows owns reconciliation.
func (s *Service) preloadSnapshot(topic string) {
	if s.db == nil {
		return
	}
	if _, ok := snapshotEligible(topic); !ok {
		return
	}
	if s.cache.Count(topic) > 0 {
		return
	}
	data, err := s.db.GetListSnapshot(topic)
	if err != nil || len(data) == 0 {
		return
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(data, &items); err != nil || len(items) == 0 {
		return
	}
	sortBy, sortOrder := s.hub.GetSortPreference(topic)
	sortCachedItems(items, sortBy, sortOrder)
	epoch := s.nextEpoch(topic)
	for _, item := range items {
		s.cache.Set(topic, item)
	}
	for i := 0; i < len(items); i += 200 {
		end := min(i+200, len(items))
		msg := &BulkListMessage{
			BaseMessage: core.BaseMessage{MessageType: "bulk_list", Timestamp: time.Now()},
			Channel:     "items",
			Topic:       topic,
			Items:       items[i:end],
			Count:       end - i,
			Epoch:       epoch,
		}
		if err := s.hub.BroadcastDirect(topic, msg); err != nil {
			log.Printf("Failed to broadcast snapshot chunk for topic %s: %v", topic, err)
		}
	}
	log.Printf("k8s watcher: preloaded %d snapshot items for %s", len(items), topic)
}

func (s *Service) saveSnapshot(topic string) {
	if s.db == nil {
		return
	}
	cluster, ok := snapshotEligible(topic)
	if !ok {
		return
	}
	items := s.cache.GetAll(topic)
	if len(items) > maxSnapshotItems {
		items = items[:maxSnapshotItems]
	}
	data, err := json.Marshal(items)
	if err != nil {
		return
	}
	if err := s.db.SaveListSnapshot(topic, cluster, data); err != nil {
		log.Printf("k8s watcher: failed to persist snapshot for %s: %v", topic, err)
	}
}

func (s *Service) watchResources(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace string, topic string) error {
	s.cache.Clear(topic)

	resource, err := s.resourceForGVR(cluster, gvr)
	if err != nil {
		if isCredErr, code, msg := isCredentialError(err); isCredErr {
			s.sendClusterErrorWithDetails(cluster, code, msg, err.Error(), true)
		}
		return err
	}

	go s.runWatchLoop(ctx, cluster, gvr, namespace, topic, resource)

	return nil
}

func (s *Service) resourceForGVR(cluster string, gvr schema.GroupVersionResource) (resourceLister, error) {
	if isNativeKind(gvr) {
		typed, err := s.client.GetClientForCluster(cluster)
		if err == nil {
			return newTypedLister(typed, gvr), nil
		}
	}
	dyn, err := s.client.GetDynamicClient(cluster)
	if err != nil {
		return nil, err
	}
	return newDynamicLister(dyn.Resource(gvr)), nil
}

func (s *Service) runWatchLoop(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace string, topic string, resource resourceLister) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] runWatchLoop for %s: %v", topic, r)
			faults.CaptureExceptionWithContext(
				fmt.Errorf("panic in runWatchLoop: %v", r),
				map[string]any{"topic": topic, "cluster": cluster, "panic": r, "stack": string(debug.Stack())},
			)
			panic(r) // Re-panic to crash
		}
	}()
	var latestRV string
	consecutiveFailures := 0
	s.preloadSnapshot(topic)

	for {
		select {
		case <-ctx.Done():
			log.Printf("k8s watcher: stopped for %s", topic)
			return
		default:
		}

		if resource == nil {
			var err error
			resource, err = s.resourceForGVR(cluster, gvr)
			if err != nil {
				log.Printf("k8s watcher: failed to create lister for %s: %v", topic, err)
				if isCredErr, code, msg := isCredentialError(err); isCredErr {
					s.sendClusterErrorWithDetails(cluster, code, msg, err.Error(), true)
				}
				if !sleepBackoff(ctx, &consecutiveFailures) {
					return
				}
				continue
			}
		}

		needsList := latestRV == ""
		var listErr error
		if needsList {
			s.syncStateMu.Lock()
			status := &syncStatus{done: make(chan struct{})}
			s.syncState[topic] = status
			s.syncStateMu.Unlock()

			latestRV, listErr = s.fetchAndBroadcastListSync(ctx, cluster, gvr, namespace, topic, resource)

			s.syncStateMu.Lock()
			if st, ok := s.syncState[topic]; ok {
				st.err = listErr
				close(st.done)
			}
			s.syncStateMu.Unlock()

			if listErr != nil {
				log.Printf("k8s watcher: list failed for %s: %v", topic, listErr)
				if isVClusterTransient(listErr, cluster) {
					s.client.EvictVClusterCaches(cluster)
					if !waitForVClusterHealthy(ctx, s.client, cluster, 30*time.Second) {
						if isCredErr, code, msg := isCredentialError(listErr); isCredErr {
							s.sendClusterErrorWithDetails(cluster, code, msg, listErr.Error(), true)
						}
					}
				} else if isCredErr, code, msg := isCredentialError(listErr); isCredErr {
					s.sendClusterErrorWithDetails(cluster, code, msg, listErr.Error(), true)
				}
				if isCredErr, _, _ := isCredentialError(listErr); isCredErr {
					resource = nil
					latestRV = ""
				}
				if consecutiveFailures == 0 {
					s.sendInitialSyncComplete(topic, 0, 0)
				}
				if !sleepBackoff(ctx, &consecutiveFailures) {
					return
				}
				continue
			}
			consecutiveFailures = 0
			s.markClusterHealthy(cluster)
			go s.saveSnapshot(topic)
		}

		watchOpts := metav1.ListOptions{Watch: true, ResourceVersion: latestRV, AllowWatchBookmarks: true}
		scoped := resource
		if namespace != "" {
			scoped = resource.Namespace(namespace)
		}
		w, err := scoped.Watch(ctx, watchOpts)
		if err != nil {
			log.Printf("k8s watcher: watch failed for %s: %v", topic, err)
			if isVClusterTransient(err, cluster) {
				s.client.EvictVClusterCaches(cluster)
				if !waitForVClusterHealthy(ctx, s.client, cluster, 30*time.Second) {
					if isCredErr, code, msg := isCredentialError(err); isCredErr {
						s.sendClusterErrorWithDetails(cluster, code, msg, err.Error(), true)
					}
				}
			} else if isCredErr, code, msg := isCredentialError(err); isCredErr {
				s.sendClusterErrorWithDetails(cluster, code, msg, err.Error(), true)
			}
			if isExpiredErr(err) {
				latestRV = ""
			}
			if isCredErr, _, _ := isCredentialError(err); isCredErr {
				resource = nil
				latestRV = ""
			}
			if !sleepBackoff(ctx, &consecutiveFailures) {
				return
			}
			continue
		}

		log.Printf("k8s watcher: started for %s from RV=%s", topic, latestRV)
		s.markClusterHealthy(cluster)

		expired, watchErr := s.processWatchEvents(ctx, w, gvr, topic, &latestRV)
		w.Stop()

		if watchErr != nil {
			if isCredErr, code, msg := isCredentialError(watchErr); isCredErr {
				s.sendClusterErrorWithDetails(cluster, code, msg, watchErr.Error(), true)
				resource = nil
				latestRV = ""
			}
			if !sleepBackoff(ctx, &consecutiveFailures) {
				return
			}
			continue
		}
		if expired {
			log.Printf("k8s watcher: RV expired for %s, forcing relist", topic)
			latestRV = ""
		}
		consecutiveFailures = 0
	}
}

func sleepBackoff(ctx context.Context, attempts *int) bool {
	*attempts++
	backoff := time.Duration(1<<min(*attempts-1, 5)) * time.Second
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(backoff):
		return true
	}
}

func isExpiredErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "too old") || strings.Contains(s, "Gone") || strings.Contains(s, "expired")
}

func (s *Service) processWatchEvents(ctx context.Context, w watch.Interface, gvr schema.GroupVersionResource, topic string, latestRV *string) (expired bool, err error) {
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case event, ok := <-w.ResultChan():
			if !ok {
				return false, nil
			}
			switch event.Type {
			case watch.Error:
				if status, ok := event.Object.(*metav1.Status); ok {
					if status.Code == 410 || status.Reason == metav1.StatusReasonExpired || status.Reason == metav1.StatusReasonGone {
						return true, nil
					}
					log.Printf("k8s watcher: watch error for %s: %s (code=%d)", topic, status.Message, status.Code)
					return false, fmt.Errorf("watch error for %s: %s (code=%d, reason=%s)", topic, status.Message, status.Code, status.Reason)
				}
				return false, fmt.Errorf("watch error for %s", topic)
			case watch.Bookmark:
				if rv := extractRV(event.Object); rv != "" {
					*latestRV = rv
				}
			default:
				if rv := extractRV(event.Object); rv != "" {
					*latestRV = rv
				}
				s.handleResourceEventSimple(event, gvr, topic)
			}
		}
	}
}

func extractRV(obj interface{}) string {
	if u, ok := obj.(*unstructured.Unstructured); ok && u != nil {
		return u.GetResourceVersion()
	}
	if m, ok := obj.(metav1.Object); ok && m != nil {
		return m.GetResourceVersion()
	}
	return ""
}

func (s *Service) fetchAndBroadcastListSync(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace string, topic string, resource resourceLister) (string, error) {
	const firstPageSize = 25
	const pageSize = 100
	sortBy, sortOrder := s.hub.GetSortPreference(topic)
	if err := s.hub.FlushTopic(topic); err != nil {
		log.Printf("Failed to flush topic %s before list sync: %v", topic, err)
	}
	epoch := s.nextEpoch(topic)
	currentItems := make(map[string]bool)
	var listResourceVersion string
	isFirstPage := true

	if gvr.Resource == "events" && s.db != nil {
		log.Printf("Fetching events from DB for cluster %s", cluster)
		events, dbErr := s.db.GetClusterEvents(cluster, 1000)
		if dbErr != nil {
			log.Printf("Failed to fetch events from DB: %v", dbErr)
			return "", dbErr
		}
		var pageItems []map[string]interface{}
		for _, e := range events {
			item := map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":              e.Name,
					"namespace":         e.Namespace,
					"uid":               e.UID,
					"creationTimestamp": e.CreatedAt.Format(time.RFC3339),
					"resourceVersion":   "",
				},
				"involvedObject": map[string]interface{}{
					"kind":            e.InvolvedObjectKind,
					"namespace":       e.InvolvedObjectNamespace,
					"name":            e.InvolvedObjectName,
					"uid":             e.InvolvedObjectUID,
					"apiVersion":      e.InvolvedObjectAPIVersion,
					"resourceVersion": "",
				},
				"type":           e.Type,
				"reason":         e.Reason,
				"message":        e.Message,
				"count":          e.Count,
				"firstTimestamp": e.FirstTimestamp.Format(time.RFC3339),
				"lastTimestamp":  e.LastTimestamp.Format(time.RFC3339),
				"eventTime":      e.EventTime.Format(time.RFC3339),
				"source": map[string]interface{}{
					"component": e.SourceComponent,
					"host":      e.SourceHost,
				},
				"kind":       "Event",
				"apiVersion": "v1",
				"name":       e.Name,
				"namespace":  e.Namespace,
				"uid":        e.UID,
				"cluster":    e.Cluster,
			}
			item["creationTimestamp"] = e.FirstTimestamp.Format(time.RFC3339)
			if e.FirstTimestamp.IsZero() {
				item["creationTimestamp"] = e.EventTime.Format(time.RFC3339)
			}
			pageItems = append(pageItems, item)
			if len(pageItems) >= pageSize {
				s.broadcastPage(topic, pageItems, currentItems, sortBy, sortOrder, isFirstPage, epoch)
				isFirstPage = false
				pageItems = pageItems[:0]
			}
		}
		if len(pageItems) > 0 {
			s.broadcastPage(topic, pageItems, currentItems, sortBy, sortOrder, isFirstPage, epoch)
		}
		listResourceVersion = "0"
	} else {
		scoped := resource
		if namespace != "" {
			scoped = resource.Namespace(namespace)
		}
		// The full list is fetched concurrently with the quick first page,
		// with ResourceVersion=0 so the apiserver serves its watch cache in a
		// single round trip — sequential Continue pages made large lists take
		// pages×RTT on slow links.
		type fullListResult struct {
			list *unstructured.UnstructuredList
			err  error
		}
		fullCh := make(chan fullListResult, 1)
		go func() {
			list, err := scoped.List(ctx, metav1.ListOptions{ResourceVersion: "0"})
			fullCh <- fullListResult{list, err}
		}()

		firstList, err := scoped.List(ctx, metav1.ListOptions{Limit: firstPageSize})
		if err != nil {
			log.Printf("k8s watcher: failed to list %s: %v", topic, err)
			if isCredErr, code, msg := isCredentialError(err); isCredErr {
				s.sendClusterErrorWithDetails(cluster, code, msg, err.Error(), true)
			}
			return "", err
		}
		if firstList != nil && len(firstList.Items) > 0 {
			pageItems := make([]map[string]interface{}, 0, len(firstList.Items))
			for i := range firstList.Items {
				pageItems = append(pageItems, simplifyUnstructuredMinimal(&firstList.Items[i], gvr))
			}
			s.broadcastPage(topic, pageItems, currentItems, sortBy, sortOrder, true, epoch)
			fullPageItems := make([]map[string]interface{}, 0, len(firstList.Items))
			for i := range firstList.Items {
				fullPageItems = append(fullPageItems, listadapters.Simplify(&firstList.Items[i], gvr))
			}
			s.broadcastPage(topic, fullPageItems, currentItems, sortBy, sortOrder, false, epoch)
		}

		res := <-fullCh
		if res.err != nil {
			log.Printf("k8s watcher: failed to list %s: %v", topic, res.err)
			if isCredErr, code, msg := isCredentialError(res.err); isCredErr {
				s.sendClusterErrorWithDetails(cluster, code, msg, res.err.Error(), true)
			}
			return "", res.err
		}
		if res.list == nil {
			return "", fmt.Errorf("list for %s returned no result", topic)
		}
		listResourceVersion = res.list.GetResourceVersion()
		for start := 0; start < len(res.list.Items); start += pageSize {
			end := min(start+pageSize, len(res.list.Items))
			pageItems := make([]map[string]interface{}, 0, end-start)
			for i := start; i < end; i++ {
				pageItems = append(pageItems, listadapters.Simplify(&res.list.Items[i], gvr))
			}
			s.broadcastPage(topic, pageItems, currentItems, sortBy, sortOrder, false, epoch)
		}
	}

	// The quick first page and the full RV=0 list overlap, so the true item
	// count is the deduplicated key set — summing page sizes double-counts.
	activeItems := len(currentItems)

	cachedItems := s.cache.GetAll(topic)
	for _, cached := range cachedItems {
		name, _ := cached["name"].(string)
		ns, _ := cached["namespace"].(string)
		key := ns + "/" + name
		if ns == "" {
			key = name
		}
		if !currentItems[key] {
			s.cache.Delete(topic, cached)
			msg := &ResourceEventMessage{
				BaseMessage: core.BaseMessage{MessageType: "event", Timestamp: time.Now()},
				Channel:     "items",
				Topic:       topic,
				Action:      "deleted",
				Item:        cached,
			}
			if err := s.hub.Broadcast(topic, msg); err != nil {
				log.Printf("Failed to broadcast stale item deletion for topic %s: %v", topic, err)
			}
			log.Printf("k8s watcher: removed stale item %s from cache for %s", key, topic)
		}
	}

	if err := s.hub.FlushTopic(topic); err != nil {
		log.Printf("Failed to flush topic %s after list broadcast: %v", topic, err)
	}
	log.Printf("k8s watcher: sent %d items for %s (RV=%s)", activeItems, topic, listResourceVersion)
	s.sendInitialSyncComplete(topic, activeItems, epoch)
	if namespace == "" {
		s.countThrottler.update(cluster, gvr.Group, gvr.Resource, activeItems)
	}
	return listResourceVersion, nil
}

func (s *Service) broadcastPage(topic string, items []map[string]interface{}, currentItems map[string]bool, sortBy, sortOrder string, isMinimal bool, epoch uint64) int {
	if len(items) == 0 {
		return 0
	}
	sortCachedItems(items, sortBy, sortOrder)
	for _, item := range items {
		name, _ := item["name"].(string)
		ns, _ := item["namespace"].(string)
		key := ns + "/" + name
		if ns == "" {
			key = name
		}
		currentItems[key] = true
		s.cache.Set(topic, item)
	}
	msg := &BulkListMessage{
		BaseMessage: core.BaseMessage{MessageType: "bulk_list", Timestamp: time.Now()},
		Channel:     "items",
		Topic:       topic,
		Items:       items,
		Count:       len(items),
		IsMinimal:   isMinimal,
	}
	if err := s.hub.BroadcastDirect(topic, msg); err != nil {
		log.Printf("Failed to broadcast bulk list for topic %s: %v", topic, err)
	}
	return len(items)
}

func (s *Service) handleResourceEventSimple(event watch.Event, gvr schema.GroupVersionResource, topic string) {
	u, ok := event.Object.(*unstructured.Unstructured)
	if !ok || u == nil {
		m, mok := event.Object.(metav1.Object)
		if !mok {
			return
		}
		item := map[string]interface{}{
			"name":      m.GetName(),
			"namespace": m.GetNamespace(),
			"uid":       string(m.GetUID()),
		}
		if rv := m.GetResourceVersion(); rv != "" {
			item["resourceVersion"] = rv
		}
		if ct := m.GetCreationTimestamp(); !ct.IsZero() {
			item["creationTimestamp"] = ct.Format(time.RFC3339)
		}
		if dt := m.GetDeletionTimestamp(); dt != nil && !dt.IsZero() {
			item["deletionTimestamp"] = dt.Format(time.RFC3339)
		}

		action := strings.ToLower(string(event.Type))
		switch action {
		case "added", "modified":
			s.cache.Set(topic, item)
		case "deleted":
			s.cache.Delete(topic, item)
		}

		msg := &ResourceEventMessage{
			BaseMessage: core.BaseMessage{MessageType: "event", Timestamp: time.Now()},
			Channel:     "items",
			Topic:       topic,
			Action:      action,
			Item:        item,
		}
		if err := s.hub.Broadcast(topic, msg); err != nil {
			log.Printf("Failed to broadcast %s event for topic %s: %v", action, topic, err)
		}
		if action == "added" || action == "deleted" {
			s.publishCountUpdate(topic, gvr)
		}
		return
	}

	item := listadapters.Simplify(u, gvr)
	action := strings.ToLower(string(event.Type))

	switch action {
	case "added", "modified":
		s.cache.Set(topic, item)
	case "deleted":
		s.cache.Delete(topic, item)
	}

	if s.invalidationBus != nil {
		cluster, _, _, _, _, ok := topics.ParseItemsTopic(topic)
		if ok {
			namespace, _ := item["namespace"].(string)
			name, _ := item["name"].(string)
			detailKey := cluster + ":" + gvr.Group + ":" + gvr.Version + ":" + gvr.Resource + ":" + namespace + ":" + name
			s.invalThrottler.add(
				"detail:"+detailKey,
				"resources:"+cluster+":*",
				"dashboard:"+cluster,
				"status:"+cluster,
			)
		}
	}

	msg := &ResourceEventMessage{
		BaseMessage: core.BaseMessage{MessageType: "event", Timestamp: time.Now()},
		Channel:     "items",
		Topic:       topic,
		Action:      action,
		Item:        item,
	}

	if err := s.hub.Broadcast(topic, msg); err != nil {
		log.Printf("Failed to broadcast error for topic %s: %v", topic, err)
	}

	if action == "added" || action == "deleted" {
		s.publishCountUpdate(topic, gvr)
	}
}

func (s *Service) publishCountUpdate(itemsTopic string, gvr schema.GroupVersionResource) {
	cluster, _, _, _, namespace, ok := topics.ParseItemsTopic(itemsTopic)
	if !ok {
		return
	}
	if namespace != "" {
		return
	}
	s.countThrottler.update(cluster, gvr.Group, gvr.Resource, s.cache.Count(itemsTopic))
}

func (s *Service) doPublishCount(cluster, group, resource string, count int) {
	countsTopic := topics.BuildCountsTopic(cluster)
	msg := &CountUpdateMessage{
		BaseMessage: core.BaseMessage{
			MessageType: "count",
			Timestamp:   time.Now(),
		},
		Channel:  "counts",
		Topic:    countsTopic,
		Group:    group,
		Resource: resource,
		Count:    count,
	}
	if err := s.hub.Broadcast(countsTopic, msg); err != nil {
		log.Printf("Failed to broadcast count update for %s/%s: %v", group, resource, err)
	}
}

type ResourceCache struct {
	mu    sync.RWMutex
	items map[string]map[string]interface{} // topic -> resourceKey -> item
}

func NewResourceCache() *ResourceCache {
	return &ResourceCache{
		items: make(map[string]map[string]interface{}),
	}
}

func (c *ResourceCache) Set(topic string, item map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.items[topic] == nil {
		c.items[topic] = make(map[string]interface{})
	}

	key := c.getItemKey(item)
	c.items[topic][key] = item
}

func (c *ResourceCache) Delete(topic string, item map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.items[topic] != nil {
		key := c.getItemKey(item)
		delete(c.items[topic], key)
	}
}

func (c *ResourceCache) Count(topic string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items[topic])
}

func (c *ResourceCache) GetAll(topic string) []map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if topicItems, ok := c.items[topic]; ok {
		result := make([]map[string]interface{}, 0, len(topicItems))
		for _, item := range topicItems {
			if itemMap, ok := item.(map[string]interface{}); ok {
				result = append(result, itemMap)
			}
		}
		return result
	}
	return nil
}

func (c *ResourceCache) Clear(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, topic)
}

func (c *ResourceCache) Topics() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.items))
	for t := range c.items {
		out = append(out, t)
	}
	return out
}

func (c *ResourceCache) getItemKey(item map[string]interface{}) string {
	name, _ := item["name"].(string)
	namespace, _ := item["namespace"].(string)
	if namespace != "" {
		return namespace + "/" + name
	}
	return name
}

const watcherGracePeriod = 5 * time.Minute

// defaultMaxWatches bounds live watches (active + grace-pending) across all
// clusters. Active watches are never evicted, so this only reclaims idle
// grace-pending watches early when many clusters/types churn. Sized generously
// so normal multi-cluster use is never affected.
const defaultMaxWatches = 512

type WatchManager struct {
	mu              sync.Mutex
	wg              sync.WaitGroup
	watches         map[string]*watchRef
	pendingCleanups map[string]*pendingCleanup
	onCleanup       func(key string)
	maxWatches      int
	activeSeq       uint64
}

type pendingCleanup struct {
	id     uint64
	ctx    context.Context
	cancel context.CancelFunc
}

type watchRef struct {
	cancel     context.CancelFunc
	refCount   int
	generation uint64
	lastActive uint64
}

// SetMaxWatches caps the number of live watches (active + grace-pending). When a
// new watch would exceed the cap, the least-recently-active grace-pending watch
// is reclaimed immediately instead of waiting out the grace period. Active
// (currently-viewed) watches are never evicted. A value <= 0 disables the cap.
func (m *WatchManager) SetMaxWatches(n int) {
	m.mu.Lock()
	m.maxWatches = n
	m.mu.Unlock()
}

func NewWatchManager() *WatchManager {
	return &WatchManager{
		watches:         make(map[string]*watchRef),
		pendingCleanups: make(map[string]*pendingCleanup),
	}
}

var cleanupIDCounter uint64

func (m *WatchManager) StartWatch(key string, watchFunc func(context.Context) error) (alreadyWatching bool, err error) {
	m.mu.Lock()

	if pc, pending := m.pendingCleanups[key]; pending {
		pc.cancel()
		delete(m.pendingCleanups, key)
		log.Printf("k8s watcher: cancelled pending cleanup for %s (re-subscribed)", key)
	}

	if wr, ok := m.watches[key]; ok {
		wr.refCount++
		m.activeSeq++
		wr.lastActive = m.activeSeq
		m.mu.Unlock()
		return true, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := watchFunc(ctx); err != nil {
		cancel()
		m.mu.Unlock()
		return false, err
	}

	m.activeSeq++
	m.watches[key] = &watchRef{cancel: cancel, refCount: 1, generation: 1, lastActive: m.activeSeq}
	evicted := m.evictOverCapLocked()
	callback := m.onCleanup
	m.mu.Unlock()

	m.runCleanupCallbacks(callback, evicted)
	return false, nil
}

// evictOverCapLocked reclaims the least-recently-active grace-pending watches
// until the live watch count is within maxWatches, returning the evicted keys.
// Active watches (refCount > 0) are never evicted. The onCleanup callback is NOT
// invoked here (it may re-enter the manager and deadlock); callers run it after
// releasing m.mu. Caller must hold m.mu.
func (m *WatchManager) evictOverCapLocked() []string {
	if m.maxWatches <= 0 || len(m.watches) <= m.maxWatches {
		return nil
	}
	var evicted []string
	for len(m.watches) > m.maxWatches {
		var victimKey string
		var victimSeq uint64
		found := false
		for key, wr := range m.watches {
			if wr.refCount > 0 {
				continue
			}
			if !found || wr.lastActive < victimSeq {
				victimKey = key
				victimSeq = wr.lastActive
				found = true
			}
		}
		if !found {
			break
		}
		wr := m.watches[victimKey]
		if pc, pending := m.pendingCleanups[victimKey]; pending {
			pc.cancel()
			delete(m.pendingCleanups, victimKey)
		}
		wr.cancel()
		delete(m.watches, victimKey)
		evicted = append(evicted, victimKey)
		log.Printf("k8s watcher: evicted grace-pending watch %s early (over cap %d)", victimKey, m.maxWatches)
	}
	return evicted
}

func (m *WatchManager) runCleanupCallbacks(callback func(key string), keys []string) {
	if callback == nil {
		return
	}
	for _, key := range keys {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("k8s watcher: panic in eviction callback for %s: %v", key, r)
				}
			}()
			callback(key)
		}()
	}
}

func (m *WatchManager) StopWatch(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	wr, ok := m.watches[key]
	if !ok {
		if pc, pending := m.pendingCleanups[key]; pending {
			pc.cancel()
			delete(m.pendingCleanups, key)
		}
		return
	}

	if wr.refCount <= 0 {
		return
	}
	wr.refCount--
	if wr.refCount > 0 {
		return
	}
	m.activeSeq++
	wr.lastActive = m.activeSeq

	if oldPc, pending := m.pendingCleanups[key]; pending {
		oldPc.cancel()
		delete(m.pendingCleanups, key)
	}

	cleanupID := atomic.AddUint64(&cleanupIDCounter, 1)
	generation := wr.generation
	ctx, cancel := context.WithCancel(context.Background())
	m.pendingCleanups[key] = &pendingCleanup{id: cleanupID, ctx: ctx, cancel: cancel}
	log.Printf("k8s watcher: scheduling cleanup for %s in %v (id=%d, gen=%d)", key, watcherGracePeriod, cleanupID, generation)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		timer := time.NewTimer(watcherGracePeriod)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.mu.Lock()
			pc, pending := m.pendingCleanups[key]
			if !pending || pc.id != cleanupID {
				m.mu.Unlock()
				return
			}
			wr, ok := m.watches[key]
			if !ok || wr.refCount > 0 || wr.generation != generation {
				delete(m.pendingCleanups, key)
				m.mu.Unlock()
				log.Printf("k8s watcher: skipping cleanup for %s (watch restarted)", key)
				return
			}
			delete(m.pendingCleanups, key)
			wr.cancel()
			delete(m.watches, key)
			callback := m.onCleanup
			m.mu.Unlock()
			log.Printf("k8s watcher: cleaned up %s after grace period", key)
			if callback != nil {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("k8s watcher: panic in cleanup callback for %s: %v", key, r)
					}
				}()
				callback(key)
			}
		}
	}()
}

func (m *WatchManager) HasWatch(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.watches[key]
	return ok
}

// StopAllPrefix cancels and removes every idle watch (refcount <= 0) whose key
// starts with prefix, returning the stopped keys. Watches with live
// subscribers are left untouched. Pending grace-period cleanups under the
// prefix are cancelled; the caller is responsible for per-topic state cleanup
// of the returned keys.
func (m *WatchManager) StopAllPrefix(prefix string) []string {
	m.mu.Lock()
	var stopped []string
	for key, wr := range m.watches {
		if !strings.HasPrefix(key, prefix) || wr.refCount > 0 {
			continue
		}
		if pc, pending := m.pendingCleanups[key]; pending {
			pc.cancel()
			delete(m.pendingCleanups, key)
		}
		wr.cancel()
		delete(m.watches, key)
		stopped = append(stopped, key)
	}
	for key, pc := range m.pendingCleanups {
		if strings.HasPrefix(key, prefix) {
			pc.cancel()
			delete(m.pendingCleanups, key)
		}
	}
	m.mu.Unlock()
	return stopped
}

func (m *WatchManager) CleanupIfNotWatching(key string, cleanupFn func()) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.watches[key]; exists {
		return false
	}
	cleanupFn()
	return true
}

func (m *WatchManager) SetOnCleanup(fn func(key string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCleanup = fn
}

func (m *WatchManager) Shutdown() {
	m.mu.Lock()
	for _, pc := range m.pendingCleanups {
		pc.cancel()
	}
	m.pendingCleanups = make(map[string]*pendingCleanup)
	for _, wr := range m.watches {
		wr.cancel()
	}
	m.watches = make(map[string]*watchRef)
	m.mu.Unlock()
	m.wg.Wait()
}

func simplifyUnstructuredMinimal(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	obj := u.Object
	item := map[string]interface{}{}
	meta, _ := obj["metadata"].(map[string]interface{})
	if meta != nil {
		item["name"], _ = meta["name"]
		item["namespace"], _ = meta["namespace"]
		item["uid"], _ = meta["uid"]
		item["creationTimestamp"], _ = meta["creationTimestamp"]
		if dt, ok := meta["deletionTimestamp"]; ok {
			item["deletionTimestamp"] = dt
		}
	}
	item["resourceVersion"] = u.GetResourceVersion()
	item["kind"] = u.GetKind()
	item["apiVersion"] = u.GetAPIVersion()
	if gvr.Resource == "pods" {
		if status, ok := obj["status"].(map[string]interface{}); ok {
			item["phase"], _ = status["phase"]
			if cs, ok := status["containerStatuses"].([]interface{}); ok {
				minCS := make([]map[string]interface{}, 0, len(cs))
				restarts := 0
				for _, v := range cs {
					if m, ok := v.(map[string]interface{}); ok {
						mc := map[string]interface{}{"name": m["name"], "state": m["state"], "ready": m["ready"]}
						if rc, ok := m["restartCount"]; ok {
							mc["restartCount"] = rc
							switch t := rc.(type) {
							case int64:
								restarts += int(t)
							case int:
								restarts += t
							case float64:
								restarts += int(t)
							}
						}
						minCS = append(minCS, mc)
					}
				}
				item["containerStatuses"] = minCS
				item["restarts"] = restarts
			}
			if ics, ok := status["initContainerStatuses"].([]interface{}); ok {
				minICS := make([]map[string]interface{}, 0, len(ics))
				for _, v := range ics {
					if m, ok := v.(map[string]interface{}); ok {
						minICS = append(minICS, map[string]interface{}{"name": m["name"], "state": m["state"]})
					}
				}
				item["initContainerStatuses"] = minICS
			}
		}
		if spec, ok := obj["spec"].(map[string]interface{}); ok {
			if containers, ok := spec["containers"].([]interface{}); ok {
				names := make([]map[string]interface{}, 0, len(containers))
				for _, c := range containers {
					if m, ok := c.(map[string]interface{}); ok {
						names = append(names, map[string]interface{}{"name": m["name"]})
					}
				}
				item["containers"] = names
			}
		}
		return item
	}
	if meta != nil {
		if labels, ok := meta["labels"]; ok {
			item["labels"] = labels
		}
		if annotations, ok := meta["annotations"].(map[string]interface{}); ok {
			if rev, ok := annotations["deployment.kubernetes.io/revision"]; ok {
				item["annotations"] = map[string]interface{}{"deployment.kubernetes.io/revision": rev}
			}
		}
	}
	if status, ok := obj["status"].(map[string]interface{}); ok {
		item["phase"], _ = status["phase"]
		item["conditions"], _ = status["conditions"]
		for _, k := range []string{"readyReplicas", "updatedReplicas", "availableReplicas", "replicas", "desiredNumberScheduled", "currentNumberScheduled", "numberReady", "updatedNumberScheduled", "numberAvailable"} {
			if v, ok := status[k]; ok {
				if k == "replicas" {
					item["statusReplicas"] = v
				} else {
					item[k] = v
				}
			}
		}
	}
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		for _, k := range []string{"replicas", "clusterIP", "type", "ports", "nodeName"} {
			if v, ok := spec[k]; ok {
				item[k] = v
			}
		}
	}
	if gvr.Resource == "secrets" {
		if t, ok := obj["type"]; ok {
			item["type"] = t
		}
	}
	if gvr.Resource == "events" {
		for _, k := range []string{"involvedObject", "source", "message", "reason", "type", "count", "firstTimestamp", "lastTimestamp", "eventTime"} {
			if v, ok := obj[k]; ok {
				item[k] = v
			}
		}
	}
	return item
}

type ResourceEventMessage struct {
	core.BaseMessage `json:",inline"`
	Channel          string                 `json:"channel"`
	Topic            string                 `json:"topic"`
	Action           string                 `json:"action"`
	Item             map[string]interface{} `json:"item"`
}

func (m *ResourceEventMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func (m *ResourceEventMessage) GetData() map[string]interface{} {
	return map[string]interface{}{
		"type":    m.MessageType,
		"channel": m.Channel,
		"topic":   m.Topic,
		"action":  m.Action,
		"item":    m.Item,
	}
}

// sendInitialSyncComplete signals list load completion. A non-zero epoch marks
// the snapshot authoritative: the client may drop rows it did not see in that
// epoch. Epoch 0 (failure paths) only clears loading state client-side.
func (s *Service) sendInitialSyncComplete(topic string, itemCount int, epoch uint64) {
	msg := &InitialSyncMessage{
		BaseMessage: core.BaseMessage{
			MessageType: "sync_complete",
			Timestamp:   time.Now(),
		},
		Channel:   "items",
		Topic:     topic,
		ItemCount: itemCount,
		Epoch:     epoch,
	}
	if err := s.hub.Broadcast(topic, msg); err != nil {
		log.Printf("Failed to broadcast sync complete for topic %s: %v", topic, err)
	} else {
		log.Printf("Sent initial sync complete for %s with %d items (epoch=%d)", topic, itemCount, epoch)
	}
}

// InitialSyncMessage indicates that initial resource loading is complete
type InitialSyncMessage struct {
	core.BaseMessage `json:",inline"`
	Channel          string `json:"channel"`
	Topic            string `json:"topic"`
	ItemCount        int    `json:"itemCount"`
	Epoch            uint64 `json:"epoch,omitempty"`
}

func (m *InitialSyncMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func (m *InitialSyncMessage) GetData() map[string]interface{} {
	data := map[string]interface{}{
		"type":      m.MessageType,
		"channel":   m.Channel,
		"topic":     m.Topic,
		"itemCount": m.ItemCount,
	}
	if m.Epoch > 0 {
		data["epoch"] = m.Epoch
	}
	return data
}

type BulkListMessage struct {
	core.BaseMessage `json:",inline"`
	Channel          string                   `json:"channel"`
	Topic            string                   `json:"topic"`
	Items            []map[string]interface{} `json:"items"`
	Count            int                      `json:"count"`
	IsMinimal        bool                     `json:"isMinimal,omitempty"`
	Epoch            uint64                   `json:"epoch,omitempty"`
}

func (m *BulkListMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func (m *BulkListMessage) GetData() map[string]interface{} {
	data := map[string]interface{}{
		"type":    m.MessageType,
		"channel": m.Channel,
		"topic":   m.Topic,
		"items":   m.Items,
		"count":   m.Count,
	}
	if m.IsMinimal {
		data["isMinimal"] = true
	}
	if m.Epoch > 0 {
		data["epoch"] = m.Epoch
	}
	return data
}

type ClusterErrorMessage struct {
	core.BaseMessage `json:",inline"`
	Cluster          string `json:"cluster"`
	ErrorCode        string `json:"errorCode"`
	ErrorMessage     string `json:"errorMessage"`
	Details          string `json:"details,omitempty"`
	Recoverable      bool   `json:"recoverable"`
}

func (m *ClusterErrorMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func (m *ClusterErrorMessage) GetData() map[string]interface{} {
	return map[string]interface{}{
		"type":         m.MessageType,
		"cluster":      m.Cluster,
		"errorCode":    m.ErrorCode,
		"errorMessage": m.ErrorMessage,
		"details":      m.Details,
		"recoverable":  m.Recoverable,
	}
}

func isCredentialError(err error) (bool, string, string) {
	if err == nil {
		return false, "", ""
	}
	code, msg, ok := k8s.ClassifyClusterError(err.Error())
	return ok, code, msg
}

func waitForVClusterHealthy(ctx context.Context, client *k8s.Client, cluster string, timeout time.Duration) bool {
	if !k8s.IsVClusterID(cluster) {
		return true
	}
	sup, ok := client.GetVClusterSupervisor(cluster)
	if !ok {
		return false
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := sup.WaitHealthy(wctx, timeout); err != nil {
		return false
	}
	return true
}

func isVClusterTransient(err error, cluster string) bool {
	if err == nil || !k8s.IsVClusterID(cluster) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "connection reset")
}

// transientErrorCodes are network/availability failures that often recover on
// their own without user action. We avoid escalating these to a full-screen
// banner on the first failure - a single slow apiserver round-trip should not
// look like the cluster is dead. Auth/cert errors fire immediately because
// they will not self-heal.
var transientErrorCodes = map[string]bool{
	"timeout":           true,
	"connection_failed": true,
	"cluster_error":     true,
}

const (
	transientFailureThreshold = 3
	transientFailureWindow    = 30 * time.Second
)

func (s *Service) sendClusterErrorWithDetails(cluster, errorCode, errorMessage, details string, recoverable bool) {
	if strings.Contains(errorCode, "expired") || strings.Contains(errorCode, "auth") || errorCode == "unauthorized" || errorCode == "token_expired" {
		s.client.RefreshClusterCache(cluster)
		log.Printf("Invalidated cached clients for cluster %s due to auth error", cluster)
	}

	if transientErrorCodes[errorCode] {
		s.clusterErrMu.Lock()
		st, ok := s.clusterErr[cluster]
		now := time.Now()
		if !ok || now.Sub(st.firstAt) > transientFailureWindow {
			st = &clusterErrState{firstAt: now}
			s.clusterErr[cluster] = st
		}
		st.failures++
		st.lastCode = errorCode
		if st.failures < transientFailureThreshold {
			s.clusterErrMu.Unlock()
			log.Printf("Suppressed transient cluster error for %s (failure %d/%d): %s", cluster, st.failures, transientFailureThreshold, errorCode)
			return
		}
		st.sent = true
		s.clusterErrMu.Unlock()
	} else {
		s.clusterErrMu.Lock()
		s.clusterErr[cluster] = &clusterErrState{failures: transientFailureThreshold, firstAt: time.Now(), lastCode: errorCode, sent: true}
		s.clusterErrMu.Unlock()
	}

	msg := &ClusterErrorMessage{
		BaseMessage: core.BaseMessage{
			MessageType: "cluster_error",
			Timestamp:   time.Now(),
		},
		Cluster:      cluster,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
		Details:      details,
		Recoverable:  recoverable,
	}
	if err := s.hub.BroadcastAll(msg); err != nil {
		log.Printf("Failed to broadcast cluster error for %s: %v", cluster, err)
	} else {
		log.Printf("Sent cluster error notification for %s: %s - %s", cluster, errorCode, errorMessage)
	}
}

// markClusterHealthy clears any tracked transient-error state for the cluster
// and, if a cluster_error was previously broadcast, broadcasts cluster_error_cleared
// so the frontend can dismiss the error banner automatically.
func (s *Service) markClusterHealthy(cluster string) {
	s.clusterErrMu.Lock()
	st, ok := s.clusterErr[cluster]
	if !ok {
		s.clusterErrMu.Unlock()
		return
	}
	wasSent := st.sent
	delete(s.clusterErr, cluster)
	s.clusterErrMu.Unlock()
	if !wasSent {
		return
	}
	msg := &ClusterErrorMessage{
		BaseMessage:  core.BaseMessage{MessageType: "cluster_error_cleared", Timestamp: time.Now()},
		Cluster:      cluster,
		ErrorCode:    "",
		ErrorMessage: "",
		Recoverable:  true,
	}
	if err := s.hub.BroadcastAll(msg); err != nil {
		log.Printf("Failed to broadcast cluster error cleared for %s: %v", cluster, err)
	} else {
		log.Printf("Cluster %s recovered; broadcast cluster_error_cleared", cluster)
	}
}
