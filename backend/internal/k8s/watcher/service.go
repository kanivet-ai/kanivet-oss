package watcher

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/faults"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/printercolumns"
	"github.com/kanivet/backend/internal/k8s/watcher/listadapters"
	"github.com/kanivet/backend/internal/topics"
	"github.com/kanivet/backend/internal/websocket/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
	// owner is the context of the watch whose list this is, guarded by
	// syncStateMu.
	owner context.Context
}

type Service struct {
	client            *k8s.Client
	printerCols       *printercolumns.Resolver
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
	snapshotMu        sync.Mutex
	snapshotSavedAt   map[string]time.Time

	// sinks are the object sinks by kind and feeds the running watches' feeds
	// by topic, both guarded by feedMu.
	feedMu sync.RWMutex
	sinks  map[string]ObjectSink
	feeds  map[string]topicFeed

	vcRecMu sync.Mutex
	vcRec   map[string]*vclusterRecovery
	// vcEvict and vcWait replace the vcluster cache eviction and health wait
	// (tests only; nil means the real client).
	vcEvict func(cluster string)
	vcWait  func(ctx context.Context, cluster string) bool
}

// vclusterRecovery is one eviction-and-wait round for a vcluster, shared by
// every topic of the cluster that failed in it.
type vclusterRecovery struct {
	done       chan struct{}
	healthy    bool
	finishedAt time.Time
}

const (
	vclusterRecoveryWait = 30 * time.Second
	// vclusterRecoveryReuse is how long a finished round still answers
	// topics that fail right after it, so they do not start another.
	vclusterRecoveryReuse = 5 * time.Second
)

// recoverVCluster evicts the vcluster's cached clients and waits for it to be
// healthy, once for all the topics that fail together. It reports whether the
// cluster became healthy; a caller whose ctx ends first gets false.
func (s *Service) recoverVCluster(ctx context.Context, cluster string) bool {
	s.vcRecMu.Lock()
	if s.vcRec == nil {
		s.vcRec = make(map[string]*vclusterRecovery)
	}
	if r := s.vcRec[cluster]; r != nil {
		select {
		case <-r.done:
			if time.Since(r.finishedAt) < vclusterRecoveryReuse {
				s.vcRecMu.Unlock()
				return r.healthy
			}
		default:
			s.vcRecMu.Unlock()
			select {
			case <-r.done:
				return r.healthy
			case <-ctx.Done():
				return false
			}
		}
	}
	r := &vclusterRecovery{done: make(chan struct{})}
	s.vcRec[cluster] = r
	s.vcRecMu.Unlock()

	// The round belongs to every waiting topic, not to the one that started
	// it, so it runs on its own and does not end with that topic's context.
	go func() {
		defer func() {
			r.finishedAt = time.Now()
			close(r.done)
		}()
		if s.vcEvict != nil {
			s.vcEvict(cluster)
		} else {
			s.client.EvictVClusterCaches(cluster)
		}
		if s.vcWait != nil {
			r.healthy = s.vcWait(context.Background(), cluster)
		} else {
			r.healthy = waitForVClusterHealthy(context.Background(), s.client, cluster, vclusterRecoveryWait)
		}
	}()
	select {
	case <-r.done:
		return r.healthy
	case <-ctx.Done():
		return false
	}
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

// TopicHolder is a Broadcaster that batches live events and can hold a
// topic's back: until release, they wait for FlushTopic.
type TopicHolder interface {
	HoldTopic(topic string) (release func())
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
	if client != nil {
		svc.printerCols = printercolumns.NewResolver(printercolumns.FetchFromDynamic(client))
	}
	manager.SetOnCleanup(func(key string) {
		cleaned := manager.CleanupIfNotWatching(key, func() {
			resourceCache.Clear(key)
			hub.CleanupTopic(key)
			svc.clearSyncState(key)
			svc.clearEpoch(key)
			svc.clearSnapshotSavedAt(key)
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

// pendingSync returns the topic's unfinished sync for the watch running under
// ctx, starting one if there is none, so sendCachedData waits for the list in
// flight instead of replaying a cache that does not hold it yet. A sync left
// unfinished by a stopped watch is taken over, waiters and all. A stopped
// watch gets one nobody waits on: a watch is cancelled before its replacement
// can start, so it never ends the replacement's sync.
func (s *Service) pendingSync(ctx context.Context, topic string) *syncStatus {
	s.syncStateMu.Lock()
	defer s.syncStateMu.Unlock()
	if ctx.Err() != nil {
		return &syncStatus{done: make(chan struct{}), owner: ctx}
	}
	if st := s.syncState[topic]; st != nil {
		select {
		case <-st.done:
		default:
			st.owner = ctx
			return st
		}
	}
	st := &syncStatus{done: make(chan struct{}), owner: ctx}
	s.syncState[topic] = st
	return st
}

// finishSync records how the watch's sync ended and releases its waiters,
// unless the watch that replaced it has taken the sync over.
func (s *Service) finishSync(ctx context.Context, st *syncStatus, err error) {
	s.syncStateMu.Lock()
	defer s.syncStateMu.Unlock()
	if st.owner != ctx {
		return
	}
	select {
	case <-st.done:
	default:
		st.err = err
		close(st.done)
	}
}

func (s *Service) clearSyncState(topic string) {
	s.syncStateMu.Lock()
	delete(s.syncState, topic)
	s.syncStateMu.Unlock()
}

// clearSnapshotSavedAt forgets when a topic's snapshot was last saved, so the
// map does not grow with every topic ever opened and a reopened topic saves
// its first fresh list.
func (s *Service) clearSnapshotSavedAt(topic string) {
	s.snapshotMu.Lock()
	delete(s.snapshotSavedAt, topic)
	s.snapshotMu.Unlock()
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
	alreadyWatching, err := s.acquireWatch(cluster, group, version, kind, namespace)
	if alreadyWatching {
		go s.sendCachedData(topic, sortBy, sortOrder)
		log.Printf("[PERF] StartWatch (cached): %v kind=%s", time.Since(t0), kind)
	} else {
		log.Printf("[PERF] StartWatch (fresh): %v kind=%s", time.Since(t0), kind)
	}
	return err
}

// acquireWatch takes a reference on the topic's watch, starting it if it is
// not running, and reports whether it was.
func (s *Service) acquireWatch(cluster, group, version, kind, namespace string) (alreadyWatching bool, err error) {
	topic := topics.BuildItemsTopic(cluster, group, version, kind, namespace)
	s.indexedClustersMu.Lock()
	needsIndexing := !s.indexedClusters[cluster]
	if needsIndexing {
		s.indexedClusters[cluster] = true
	}
	s.indexedClustersMu.Unlock()
	if needsIndexing && s.onClusterWatched != nil {
		go s.onClusterWatched(cluster)
	}
	return s.manager.StartWatch(topic, func(ctx context.Context) error {
		s.watchResources(ctx, cluster, group, version, kind, namespace, topic)
		return nil
	})
}

// IsWatching reports whether a live (or grace-pending) cluster-wide watch exists
// for the resource, meaning its events already keep downstream consumers fresh.
func (s *Service) IsWatching(cluster, group, version, resource string) bool {
	return s.manager.HasWatch(topics.BuildItemsTopic(cluster, group, version, resource, ""))
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
		s.clearSnapshotSavedAt(topic)
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

// cachedDataSyncWait is how long a late subscriber waits for the topic's first
// list before it is told to stop loading.
var cachedDataSyncWait = 30 * time.Second

func (s *Service) sendCachedData(topic, sortBy, sortOrder string) {
	t0 := time.Now()
	s.syncStateMu.RLock()
	status := s.syncState[topic]
	s.syncStateMu.RUnlock()
	if status != nil {
		select {
		case <-status.done:
			if status.err != nil {
				// The first list failed: there is no snapshot to replay, but
				// the subscriber must still leave its loading state. This is
				// the same signal a failing list sends to the first one.
				if s.manager.HasWatch(topic) {
					s.sendInitialSyncComplete(topic, 0, 0)
				}
				return
			}
		case <-time.After(cachedDataSyncWait):
			if s.manager.HasWatch(topic) {
				s.sendInitialSyncComplete(topic, 0, 0)
			}
			return
		}
	}
	if !s.manager.HasWatch(topic) {
		return
	}
	// Live events go out after the snapshot's pages, as each page may be
	// older than they are: held from before the snapshot is read until
	// sync_complete, they are flushed just ahead of it.
	if h, ok := s.hub.(TopicHolder); ok {
		defer h.HoldTopic(topic)()
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
		if err := s.broadcastBulk(msg); err != nil {
			log.Printf("Failed to broadcast cached data chunk for topic %s: %v", topic, err)
		}
		i = end
	}
	s.sendInitialSyncComplete(topic, len(cachedItems), epoch)
	log.Printf("[PERF] sendCachedData: %d items in %v (epoch=%d)", len(cachedItems), time.Since(t0), epoch)
}

// broadcastBulk sends a bulk_list chunk. A connection refuses a message over
// its size limit as a whole (core.ErrInvalidMessage) and nothing resends it,
// so an oversized chunk is split in half until each part fits; only a single
// item that is too large on its own is dropped.
func (s *Service) broadcastBulk(msg *BulkListMessage) error {
	err := s.hub.BroadcastDirect(msg.Topic, msg)
	if len(msg.Items) < 2 || !errors.Is(err, core.ErrInvalidMessage) {
		return err
	}
	half := len(msg.Items) / 2
	first, second := *msg, *msg
	first.Items, first.Count = msg.Items[:half], half
	second.Items, second.Count = msg.Items[half:], len(msg.Items)-half
	return errors.Join(s.broadcastBulk(&first), s.broadcastBulk(&second))
}

// sortKey is one item's sort value, computed once per item rather than on
// every comparison: lowercasing both operands per comparison cost 50k rows
// two allocations on each of their n·log n comparisons.
type sortKey struct {
	str   string
	num   float64
	isNum bool
	isNil bool
}

// sortKeyOf orders strings case-insensitively unless lower is false, numbers
// numerically and nil before everything else. Other values compare as an
// empty string.
func sortKeyOf(v interface{}, lower bool) sortKey {
	switch t := v.(type) {
	case nil:
		return sortKey{isNil: true}
	case string:
		if lower {
			t = strings.ToLower(t)
		}
		return sortKey{str: t}
	case int:
		return sortKey{num: float64(t), isNum: true}
	case int64:
		return sortKey{num: float64(t), isNum: true}
	case float64:
		return sortKey{num: t, isNum: true}
	}
	return sortKey{}
}

func (a sortKey) compare(b sortKey) int {
	switch {
	case a.isNil && b.isNil:
		return 0
	case a.isNil:
		return -1
	case b.isNil:
		return 1
	case a.isNum && b.isNum:
		if a.num < b.num {
			return -1
		}
		if a.num > b.num {
			return 1
		}
		return 0
	}
	return strings.Compare(a.str, b.str)
}

func sortCachedItems(items []map[string]interface{}, sortBy, sortOrder string) {
	if len(items) < 2 {
		return
	}
	field := sortBy
	// RFC3339 timestamps order correctly byte-wise, so they skip lowercasing.
	timestamp := sortBy == "age" || sortBy == "creationTimestamp"
	if timestamp {
		field = "creationTimestamp"
	}
	type keyed struct {
		key             sortKey
		namespace, name string
		item            map[string]interface{}
	}
	ks := make([]keyed, len(items))
	for i, item := range items {
		ns, _ := item["namespace"].(string)
		name, _ := item["name"].(string)
		ks[i] = keyed{key: sortKeyOf(item[field], !timestamp), namespace: ns, name: name, item: item}
	}
	desc := sortOrder == "desc"
	slices.SortFunc(ks, func(a, b keyed) int {
		c := a.key.compare(b.key)
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		// Ties (objects created in the same second) fall back to namespace
		// and name, so the same items always come out in the same order.
		if c = strings.Compare(a.namespace, b.namespace); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	for i := range ks {
		items[i] = ks[i].item
	}
}

const maxSnapshotItems = 2500

// snapshotEligible reports whether a topic's items should be persisted for
// stale-then-fresh cold starts. Core events expire within an hour, so a
// snapshot from an earlier session would mostly hold events that are gone.
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
	if err := jsonv2.Unmarshal(data, &items); err != nil || len(items) == 0 {
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
		if err := s.broadcastBulk(msg); err != nil {
			log.Printf("Failed to broadcast snapshot chunk for topic %s: %v", topic, err)
		}
	}
	log.Printf("k8s watcher: preloaded %d snapshot items for %s", len(items), topic)
}

// snapshotSaveInterval spaces out the snapshot writes that follow a LIST, so
// a topic that relists often does not re-encode and rewrite up to 2,500 rows
// each time. Shutdown still writes every topic's latest state.
const snapshotSaveInterval = time.Minute

// saveSnapshotSoon persists the topic's snapshot in the background unless it
// was saved less than snapshotSaveInterval ago.
func (s *Service) saveSnapshotSoon(topic string) {
	if s.db == nil {
		return
	}
	s.snapshotMu.Lock()
	if time.Since(s.snapshotSavedAt[topic]) < snapshotSaveInterval {
		s.snapshotMu.Unlock()
		return
	}
	if s.snapshotSavedAt == nil {
		s.snapshotSavedAt = make(map[string]time.Time)
	}
	s.snapshotSavedAt[topic] = time.Now()
	s.snapshotMu.Unlock()
	go s.saveSnapshot(topic)
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
		// The cache is a map: without sorting first, a long list would
		// persist a random subset instead of the rows the user sees first.
		sortBy, sortOrder := s.hub.GetSortPreference(topic)
		sortCachedItems(items, sortBy, sortOrder)
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

// watchResources starts the topic's watch loop. Resolving the resource name
// (a discovery round trip on a cache miss) and building the client (which, for
// a reconnecting vcluster, waits up to 25 s for its tunnel) happen in that
// goroutine, not here: StartWatch runs on the connection's only read loop and
// under the manager's lock, so doing them inline stalled every other message
// on the socket and every other topic's subscribe. runWatchLoop retries a
// client it cannot build with backoff and reports credential errors.
func (s *Service) watchResources(ctx context.Context, cluster, group, version, kind, namespace, topic string) {
	s.cache.Clear(topic)
	// The first list is pending from here, not only once the goroutine gets
	// to it, so a revisit in the meantime waits for it rather than replaying
	// the still-empty cache as an authoritative snapshot.
	pending := s.pendingSync(ctx, topic)
	go func() {
		gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: s.client.GetResourceName(cluster, group, version, kind)}
		if ctx.Err() != nil {
			s.finishSync(ctx, pending, ctx.Err())
			return
		}
		s.runWatchLoop(ctx, cluster, gvr, namespace, topic, nil)
	}()
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
	if typed, err := s.client.GetClientForCluster(cluster); err == nil {
		return newStreamingDynamicLister(dyn.Resource(gvr), typed.CoreV1().RESTClient(), gvr), nil
	}
	return newDynamicLister(dyn.Resource(gvr)), nil
}

const maxPanicRestarts = 5

// panicRestartBackoff is the first wait before a panicked loop restarts; it
// doubles per consecutive panic.
var panicRestartBackoff = time.Second

// runWatchLoop keeps one topic's watch going. A panic in it is logged and the
// loop restarted with backoff, a bounded number of times in a row, instead of
// crashing the process and with it every other cluster's watches.
func (s *Service) runWatchLoop(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace string, topic string, resource resourceLister) {
	defer s.openFeed(ctx, topic)()
	delay := panicRestartBackoff
	for restarts := 0; ; {
		started := time.Now()
		panicked := s.runWatchLoopGuarded(ctx, cluster, gvr, namespace, topic, resource)
		if !panicked || ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			// It ran for a while before failing: not a crash loop.
			restarts, delay = 0, panicRestartBackoff
		}
		restarts++
		if restarts >= maxPanicRestarts {
			log.Printf("k8s watcher: giving up on %s after %d panics", topic, restarts)
			err := fmt.Errorf("watch for %s keeps failing", topic)
			s.finishSync(ctx, s.pendingSync(ctx, topic), err)
			s.sendInitialSyncComplete(topic, 0, 0)
			return
		}
		log.Printf("k8s watcher: restarting %s in %v after a panic", topic, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
}

// runWatchLoopGuarded runs the loop once, reporting whether it panicked.
func (s *Service) runWatchLoopGuarded(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace string, topic string, resource resourceLister) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			log.Printf("[PANIC] runWatchLoop for %s: %v\n%s", topic, r, debug.Stack())
			faults.CaptureExceptionWithContext(
				fmt.Errorf("panic in runWatchLoop: %v", r),
				map[string]any{"topic": topic, "cluster": cluster, "panic": r, "stack": string(debug.Stack())},
			)
		}
	}()
	s.watchLoop(ctx, cluster, gvr, namespace, topic, resource)
	return false
}

func (s *Service) watchLoop(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace string, topic string, resource resourceLister) {
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
				if consecutiveFailures == 0 {
					// Like a failed list: revisits must not replay the empty
					// cache as authoritative, and the UI leaves its loading
					// state while the client is retried.
					s.finishSync(ctx, s.pendingSync(ctx, topic), err)
					s.sendInitialSyncComplete(topic, 0, 0)
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
			status := s.pendingSync(ctx, topic)
			latestRV, listErr = s.fetchAndBroadcastListSync(ctx, cluster, gvr, namespace, topic, resource)
			s.finishSync(ctx, status, listErr)

			if listErr != nil {
				log.Printf("k8s watcher: list failed for %s: %v", topic, listErr)
				if isVClusterTransient(listErr, cluster) {
					if !s.recoverVCluster(ctx, cluster) {
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
			s.saveSnapshotSoon(topic)
		}

		// A half-open connection delivers nothing and never errors, so each
		// watch asks the server to end it after a jittered 5-10 minutes (the
		// loop resumes from latestRV, no relist), and a client side deadline
		// just past that recycles it when the server cannot answer.
		watchSeconds := watchTimeoutSeconds()
		watchOpts := metav1.ListOptions{Watch: true, ResourceVersion: latestRV, AllowWatchBookmarks: true, TimeoutSeconds: &watchSeconds}
		scoped := resource
		if namespace != "" {
			scoped = resource.Namespace(namespace)
		}
		watchCtx, cancelWatch := context.WithTimeout(ctx, time.Duration(watchSeconds)*time.Second+watchClientGrace)
		w, err := scoped.Watch(watchCtx, watchOpts)
		if err != nil {
			cancelWatch()
			log.Printf("k8s watcher: watch failed for %s: %v", topic, err)
			if isVClusterTransient(err, cluster) {
				if !s.recoverVCluster(ctx, cluster) {
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

		watchStart := time.Now()
		events, expired, watchErr := s.processWatchEvents(ctx, w, cluster, gvr, topic, &latestRV)
		w.Stop()
		cancelWatch()

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
		} else if events == 0 && time.Since(watchStart) < time.Second && ctx.Err() == nil {
			// A watch closed right after it opened, with nothing delivered,
			// is a failure (client-go's reflector treats it the same way): a
			// server, proxy or dying port-forward that drops every watch
			// would otherwise be re-watched in a tight loop, since watches
			// are not client-side rate limited.
			log.Printf("k8s watcher: watch for %s closed immediately, backing off", topic)
			if !sleepBackoff(ctx, &consecutiveFailures) {
				return
			}
			continue
		}
		consecutiveFailures = 0
	}
}

const (
	watchClientGrace = 30 * time.Second
	// A list that takes longer is a stuck connection, not a big list: the
	// first page is 25 items, the full list is served from the watch cache.
	firstPageListTimeout = time.Minute
	fullListTimeout      = 5 * time.Minute
)

// watchTimeoutSeconds is a watch's server side lifetime: 5-10 minutes, so the
// watches of a cluster do not all end (and reconnect) together.
func watchTimeoutSeconds() int64 {
	return 300 + rand.Int64N(301)
}

func sleepBackoff(ctx context.Context, attempts *int) bool {
	*attempts++
	select {
	case <-ctx.Done():
		return false
	case <-time.After(backoffDelay(*attempts)):
		return true
	}
}

// backoffDelay doubles from 1 s up to 30 s per attempt, with equal jitter: a
// cluster's topics usually fail together (an apiserver restart drops all
// their watches), and spreading their retries keeps them from reconnecting
// and relisting in lockstep.
func backoffDelay(attempt int) time.Duration {
	backoff := time.Duration(1<<min(attempt-1, 5)) * time.Second
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	return backoff/2 + rand.N(backoff/2)
}

func isExpiredErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "too old") || strings.Contains(s, "Gone") || strings.Contains(s, "expired")
}

// processWatchEvents applies a watch's events until it ends, returning how
// many it delivered (bookmarks included).
func (s *Service) processWatchEvents(ctx context.Context, w watch.Interface, cluster string, gvr schema.GroupVersionResource, topic string, latestRV *string) (events int, expired bool, err error) {
	for {
		select {
		case <-ctx.Done():
			return events, false, nil
		case event, ok := <-w.ResultChan():
			if !ok {
				return events, false, nil
			}
			switch event.Type {
			case watch.Error:
				if status, ok := event.Object.(*metav1.Status); ok {
					if status.Code == 410 || status.Reason == metav1.StatusReasonExpired || status.Reason == metav1.StatusReasonGone {
						return events, true, nil
					}
					log.Printf("k8s watcher: watch error for %s: %s (code=%d)", topic, status.Message, status.Code)
					return events, false, fmt.Errorf("watch error for %s: %s (code=%d, reason=%s)", topic, status.Message, status.Code, status.Reason)
				}
				return events, false, fmt.Errorf("watch error for %s", topic)
			case watch.Bookmark:
				events++
				if rv := extractRV(event.Object); rv != "" {
					*latestRV = rv
				}
			default:
				events++
				if rv := extractRV(event.Object); rv != "" {
					*latestRV = rv
				}
				s.handleResourceEventSimple(ctx, event, cluster, gvr, topic)
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

// listPageSize is how many rows one page of a list sync carries.
const listPageSize = 100

func (s *Service) fetchAndBroadcastListSync(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace string, topic string, resource resourceLister) (string, error) {
	const firstPageSize = 25
	sortBy, sortOrder := s.hub.GetSortPreference(topic)
	if err := s.hub.FlushTopic(topic); err != nil {
		log.Printf("Failed to flush topic %s before list sync: %v", topic, err)
	}
	epoch := s.nextEpoch(topic)
	currentItems := make(map[string]bool)

	// Events are listed from the cluster like every other kind: the event
	// listener's store keeps rows for 30 days whether or not the events still
	// exist.
	scoped := resource
	if namespace != "" {
		scoped = resource.Namespace(namespace)
	}
	cols := sync.OnceValue(func() (cols []printercolumns.Column) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] printer columns for %s: %v\n%s", topic, r, debug.Stack())
				cols = nil
			}
		}()
		return s.printerColumnsFor(ctx, cluster, gvr)
	})
	go cols()

	// The full list is fetched concurrently with the quick first page,
	// with ResourceVersion=0 so the apiserver serves its watch cache in a
	// single round trip — sequential Continue pages made large lists take
	// pages×RTT on slow links. Its rows are sent on as they download: a list
	// held back until its last byte left a slow link showing the first page
	// alone for seconds.
	listCtx, cancelList := context.WithTimeout(ctx, fullListTimeout)
	rest := s.startFullList(listCtx, scoped, gvr, topic, cols, s.feedFor(ctx, topic))
	// Returning early (the first page failed) cancels the full list
	// instead of downloading a whole list nobody reads.
	defer rest.stop(cancelList)

	firstCtx, cancelFirst := context.WithTimeout(ctx, firstPageListTimeout)
	firstList, err := scoped.List(firstCtx, metav1.ListOptions{Limit: firstPageSize})
	cancelFirst()
	if err != nil {
		log.Printf("k8s watcher: failed to list %s: %v", topic, err)
		if isCredErr, code, msg := isCredentialError(err); isCredErr {
			s.sendClusterErrorWithDetails(cluster, code, msg, err.Error(), true)
		}
		return "", err
	}
	total := listTotal(firstList)
	// Items already sent in the full first page are skipped in the RV=0
	// sweep unless their resourceVersion moved in between.
	sentRV := make(map[string]string)
	if firstList != nil && len(firstList.Items) > 0 {
		pre := listadapters.IsPresimplified(firstList)
		pageItems := make([]map[string]interface{}, 0, len(firstList.Items))
		fullPageItems := make([]map[string]interface{}, 0, len(firstList.Items))
		for i := range firstList.Items {
			var full, minimal map[string]interface{}
			if pre {
				full = firstList.Items[i].Object
				minimal = listadapters.MinimalProjection(full)
			} else {
				full = s.simplifyListed(&firstList.Items[i], gvr, cols())
				minimal = simplifyUnstructuredMinimal(&firstList.Items[i], gvr)
				copyPrinterColumns(minimal, full)
			}
			pageItems = append(pageItems, minimal)
			fullPageItems = append(fullPageItems, full)
			rv, _ := full["resourceVersion"].(string)
			sentRV[itemKeyOf(full)] = rv
		}
		// Membership comes from the full list alone: the watch resumes
		// from its resourceVersion, so an object the first page saw but
		// that was deleted before the full list would never get a delete
		// event. The stale diff below removes it instead.
		s.broadcastPage(topic, pageItems, nil, sortBy, sortOrder, true, epoch, total)
		s.broadcastPage(topic, fullPageItems, nil, sortBy, sortOrder, false, epoch, total)
	}

	for page := range rest.pages {
		pageItems := page[:0]
		for _, item := range page {
			key := itemKeyOf(item)
			currentItems[key] = true
			if rv, seen := sentRV[key]; seen && rv == item["resourceVersion"] {
				continue
			}
			pageItems = append(pageItems, item)
		}
		s.broadcastPage(topic, pageItems, currentItems, sortBy, sortOrder, false, epoch, total)
	}
	if rest.err != nil {
		log.Printf("k8s watcher: failed to list %s: %v", topic, rest.err)
		if isCredErr, code, msg := isCredentialError(rest.err); isCredErr {
			s.sendClusterErrorWithDetails(cluster, code, msg, rest.err.Error(), true)
		}
		return "", rest.err
	}
	listResourceVersion := rest.rv

	// The quick first page and the full RV=0 list overlap, so the true item
	// count is the full list's key set — summing page sizes double-counts.
	activeItems := len(currentItems)

	cachedItems := s.cache.GetAll(topic)
	for _, cached := range cachedItems {
		key := itemKeyOf(cached)
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

// listTotal is how many items the list a first page opens holds in all, or 0
// when the server did not say.
func listTotal(first *unstructured.UnstructuredList) int {
	if first == nil {
		return 0
	}
	if first.GetContinue() == "" {
		return len(first.Items)
	}
	if left := first.GetRemainingItemCount(); left != nil {
		return len(first.Items) + int(*left)
	}
	return 0
}

// fullList is a list in flight. Its rows arrive on pages as they are decoded;
// once pages is closed, rv and err say how the list ended.
type fullList struct {
	pages chan []map[string]interface{}
	rv    string
	err   error
}

// startFullList lists everything in the background, a page of rows at a time.
func (s *Service) startFullList(ctx context.Context, lister resourceLister, gvr schema.GroupVersionResource, topic string, cols func() []printercolumns.Column, feed ObjectFeed) *fullList {
	l := &fullList{pages: make(chan []map[string]interface{}, 2)}
	go func() {
		defer close(l.pages)
		// A panic here is in a goroutine the loop's recover does not cover.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] full list for %s: %v\n%s", topic, r, debug.Stack())
				l.err = fmt.Errorf("list for %s panicked: %v", topic, r)
			}
		}()
		page := make([]map[string]interface{}, 0, listPageSize)
		l.rv, l.err = s.listRows(ctx, lister, gvr, topic, cols, feed, func(row map[string]interface{}) error {
			page = append(page, row)
			if len(page) < listPageSize {
				return nil
			}
			ready := page
			page = make([]map[string]interface{}, 0, listPageSize)
			return l.send(ctx, ready)
		})
		if l.err == nil && len(page) > 0 {
			l.err = l.send(ctx, page)
		}
	}()
	return l
}

// send hands a page over, or gives up when the list is abandoned: nobody
// reads pages then.
func (l *fullList) send(ctx context.Context, page []map[string]interface{}) error {
	select {
	case l.pages <- page:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stop abandons the list and waits for it to end, so that none of it is still
// being handed over once the caller has moved on.
func (l *fullList) stop(cancel context.CancelFunc) {
	cancel()
	for range l.pages {
	}
}

// listRows lists everything the lister has, calling row with each item's list
// row, and returns the list's resourceVersion. A lister that reads its list
// as a stream gets its rows out while the list is still downloading. The
// topic's feed, if it has one, is given the listing's objects.
func (s *Service) listRows(ctx context.Context, lister resourceLister, gvr schema.GroupVersionResource, topic string, cols func() []printercolumns.Column, feed ObjectFeed, row func(map[string]interface{}) error) (string, error) {
	opts := metav1.ListOptions{ResourceVersion: "0"}
	if items, ok := lister.(itemLister); ok {
		feed.Reset()
		rv, err := items.ListEach(ctx, opts, func(obj runtime.Object) error {
			feed.Put(obj)
			if item := s.rowOf(obj, gvr, cols); item != nil {
				return row(item)
			}
			return nil
		})
		if err == nil {
			feed.Synced()
		}
		return rv, err
	}
	list, err := lister.List(ctx, opts)
	if err != nil {
		return "", err
	}
	if list == nil {
		return "", fmt.Errorf("list for %s returned no result", topic)
	}
	// A list that comes simplified already holds rows, not objects: there is
	// nothing in it for a feed.
	pre := listadapters.IsPresimplified(list)
	if !pre {
		feed.Reset()
	}
	for i := range list.Items {
		item := list.Items[i].Object
		if !pre {
			feed.Put(&list.Items[i])
			item = s.simplifyListed(&list.Items[i], gvr, cols())
		}
		if err := row(item); err != nil {
			return "", err
		}
	}
	if !pre {
		feed.Synced()
	}
	return list.GetResourceVersion(), nil
}

// rowOf builds an object's list row: the typed adapter's where the object has
// one, the unstructured adapters' with the kind's printer columns otherwise.
// It returns nil for an object neither can read.
func (s *Service) rowOf(obj runtime.Object, gvr schema.GroupVersionResource, cols func() []printercolumns.Column) map[string]interface{} {
	if item, ok := listadapters.SimplifyTyped(obj, gvr); ok {
		return item
	}
	if u, ok := obj.(*unstructured.Unstructured); ok && u != nil {
		return s.simplifyListed(u, gvr, cols())
	}
	return nil
}

// broadcastPage caches and sends one page of a list sync, recording its keys
// in currentItems unless that is nil. Every page carries the sync's epoch, so
// the client can drop the rows the closing sync_complete no longer covers,
// and the size of the whole list when the server gave it, so the client can
// tell how much of the list it has.
func (s *Service) broadcastPage(topic string, items []map[string]interface{}, currentItems map[string]bool, sortBy, sortOrder string, isMinimal bool, epoch uint64, total int) int {
	if len(items) == 0 {
		return 0
	}
	sortCachedItems(items, sortBy, sortOrder)
	for _, item := range items {
		if currentItems != nil {
			currentItems[itemKeyOf(item)] = true
		}
		s.cache.Set(topic, item)
	}
	msg := &BulkListMessage{
		BaseMessage: core.BaseMessage{MessageType: "bulk_list", Timestamp: time.Now()},
		Channel:     "items",
		Topic:       topic,
		Items:       items,
		Count:       len(items),
		IsMinimal:   isMinimal,
		Epoch:       epoch,
		Total:       total,
	}
	if err := s.broadcastBulk(msg); err != nil {
		log.Printf("Failed to broadcast bulk list for topic %s: %v", topic, err)
	}
	return len(items)
}

func (s *Service) handleResourceEventSimple(ctx context.Context, event watch.Event, cluster string, gvr schema.GroupVersionResource, topic string) {
	action := strings.ToLower(string(event.Type))
	if event.Type == watch.Deleted {
		s.feedFor(ctx, topic).Delete(event.Object)
	} else {
		s.feedFor(ctx, topic).Put(event.Object)
	}
	cols := func() []printercolumns.Column { return s.printerColumnsFor(ctx, cluster, gvr) }
	if item := s.rowOf(event.Object, gvr, cols); item != nil {
		s.emitResourceEvent(topic, gvr, action, item, true)
		return
	}
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
	s.emitResourceEvent(topic, gvr, action, item, false)
}

func (s *Service) printerColumnsFor(ctx context.Context, cluster string, gvr schema.GroupVersionResource) []printercolumns.Column {
	if s == nil || s.printerCols == nil || listadapters.HasAdapter(gvr) {
		return nil
	}
	return s.printerCols.Columns(ctx, cluster, gvr)
}

func (s *Service) simplifyListed(u *unstructured.Unstructured, gvr schema.GroupVersionResource, cols []printercolumns.Column) map[string]interface{} {
	item := listadapters.Simplify(u, gvr)
	if u != nil {
		printercolumns.Attach(item, printercolumns.Evaluate(u.Object, cols))
	}
	return item
}

func copyPrinterColumns(dst, src map[string]interface{}) {
	if dst == nil || src == nil {
		return
	}
	if v, ok := src["printerColumns"]; ok {
		dst["printerColumns"] = v
	}
}

func (s *Service) emitResourceEvent(topic string, gvr schema.GroupVersionResource, action string, item map[string]interface{}, invalidate bool) {
	switch action {
	case "added", "modified":
		s.cache.Set(topic, item)
	case "deleted":
		s.cache.Delete(topic, item)
	}

	if invalidate && s.invalidationBus != nil {
		cluster, _, _, _, _, ok := topics.ParseItemsTopic(topic)
		if ok {
			namespace, _ := item["namespace"].(string)
			name, _ := item["name"].(string)
			// Same shape as the API's detail cache key, which is keyed on the
			// resolved resource name too.
			detailKey := cluster + ":" + gvr.Group + ":" + gvr.Version + ":" + gvr.Resource + ":" + namespace + ":" + name
			patterns := []string{"detail:" + detailKey, "dashboard:" + cluster}
			// Only creations and deletions change the cached category
			// counts, and of the cluster status only its node and namespace
			// counts depend on objects at all.
			if action == "added" || action == "deleted" {
				patterns = append(patterns, "resources:"+cluster+":*")
				if gvr.Group == "" && (gvr.Resource == "nodes" || gvr.Resource == "namespaces") {
					patterns = append(patterns, "status:"+cluster)
				}
			}
			s.invalThrottler.add(patterns...)
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
	return itemKeyOf(item)
}

func itemKeyOf(item map[string]interface{}) string {
	name, _ := item["name"].(string)
	if namespace, _ := item["namespace"].(string); namespace != "" {
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
// epoch. Epoch 0 (failure paths) only clears loading state client-side. It is
// sent directly, behind the bulk pages and any flushed events, rather than
// waiting out the event batcher's window.
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
	if err := s.hub.FlushTopic(topic); err != nil {
		log.Printf("Failed to flush topic %s before sync complete: %v", topic, err)
	}
	if err := s.hub.BroadcastDirect(topic, msg); err != nil {
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
	// Total is how many items the list being synced holds, when known.
	Total int `json:"total,omitempty"`
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
	if m.Total > 0 {
		data["total"] = m.Total
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
