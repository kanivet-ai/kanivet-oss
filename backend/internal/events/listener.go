package events

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

type EventListener struct {
	k8sClient  k8s.Interface
	db         *db.DB
	listeners  map[string]*clusterListener
	mu         sync.RWMutex
	windowSize int
	// released holds the clusters stopped by StopListening (their tab was
	// closed) and not started explicitly since; RecentEvents leaves them be.
	released map[string]bool
	// closed is set by StopAll at shutdown, after which nothing starts.
	closed bool
}

// errReleased is why RecentEvents does not start a released cluster.
var errReleased = errors.New("cluster was released")

type clusterListener struct {
	cluster  string
	client   kubernetes.Interface
	cancel   context.CancelFunc
	isActive bool
	// starting is set while the listener's client is being built.
	starting bool
	lastSync time.Time
	// synced is set once the initial list has been stored, from when the
	// database holds the cluster's current events.
	synced atomic.Bool
}

func NewEventListener(k8sClient k8s.Interface, database *db.DB) *EventListener {
	return &EventListener{
		k8sClient:  k8sClient,
		db:         database,
		listeners:  make(map[string]*clusterListener),
		windowSize: 10000,
		released:   make(map[string]bool),
	}
}

// StartListening starts the cluster's listener for a view of the cluster
// being opened, which also undoes an earlier StopListening's release.
func (el *EventListener) StartListening(cluster string) error {
	return el.start(cluster, true)
}

// start starts the cluster's listener. Only an explicit start may start a
// released cluster: an automatic one comes from a dashboard refresh that can
// still be running when the cluster's tab closes, and must not bring back the
// listener the close just stopped. Building the client (up to 25 s for a
// reconnecting vcluster) and recording the start run outside el.mu, which
// IsListening takes on every dashboard refresh of every cluster.
func (el *EventListener) start(cluster string, explicit bool) error {
	el.mu.Lock()
	if el.closed {
		el.mu.Unlock()
		return fmt.Errorf("event listener for cluster %s: shutting down", cluster)
	}
	if explicit {
		delete(el.released, cluster)
	} else if el.released[cluster] {
		el.mu.Unlock()
		return errReleased
	}
	if listener, exists := el.listeners[cluster]; exists && (listener.isActive || listener.starting) {
		el.mu.Unlock()
		log.Printf("Event listener for cluster %s is already active", cluster)
		return nil
	}
	listener := &clusterListener{cluster: cluster, starting: true}
	el.listeners[cluster] = listener
	el.mu.Unlock()

	client, err := el.k8sClient.GetClientForCluster(cluster)

	el.mu.Lock()
	if el.listeners[cluster] != listener {
		// Stopped while its client was built.
		el.mu.Unlock()
		return nil
	}
	if err != nil {
		delete(el.listeners, cluster)
		el.mu.Unlock()
		return fmt.Errorf("failed to get client for cluster %s: %v", cluster, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	listener.client = client
	listener.cancel = cancel
	listener.isActive = true
	listener.starting = false
	listener.lastSync = time.Now()
	el.mu.Unlock()

	if el.db != nil {
		if _, err := el.db.CreateOrUpdateEventListener(cluster); err != nil {
			log.Printf("Failed to create/update event listener record for cluster %s: %v", cluster, err)
		}
	}

	go el.watchEvents(ctx, listener)

	log.Printf("Started event listener for cluster %s", cluster)
	return nil
}

func (el *EventListener) IsListening(cluster string) bool {
	el.mu.RLock()
	defer el.mu.RUnlock()

	listener, exists := el.listeners[cluster]
	return exists && listener.isActive
}

// RecentEvents returns the cluster's n most recent stored events, starting the
// cluster's listener if it is not running and was not released. It reports
// false until that listener has stored its initial list: rows kept from an
// earlier session are up to 30 days old and would hide what is happening now.
func (el *EventListener) RecentEvents(cluster string, n int) ([]db.K8sEvent, bool) {
	if el.db == nil {
		return nil, false
	}
	if !el.IsListening(cluster) {
		if err := el.start(cluster, false); err != nil {
			if !errors.Is(err, errReleased) {
				log.Printf("Failed to start event listener for cluster %s: %v", cluster, err)
			}
			return nil, false
		}
	}
	el.mu.RLock()
	listener := el.listeners[cluster]
	el.mu.RUnlock()
	if listener == nil || !listener.synced.Load() {
		return nil, false
	}
	events, err := el.db.GetRecentClusterEvents(cluster, n)
	if err != nil {
		log.Printf("Failed to read recent events for cluster %s: %v", cluster, err)
		return nil, false
	}
	return events, true
}

// watchEvents lists once, then watches from the list's resourceVersion and keeps
// following the latest seen version across reconnects. Watching without a
// version made the apiserver replay every existing event on each reconnect.
func (el *EventListener) watchEvents(ctx context.Context, listener *clusterListener) {
	log.Printf("Starting event watcher for cluster %s", listener.cluster)
	rv := ""
	for {
		select {
		case <-ctx.Done():
			log.Printf("Event watcher context cancelled for cluster %s", listener.cluster)
			return
		default:
		}
		if rv == "" {
			listRV, err := el.syncExistingEvents(ctx, listener)
			if err != nil {
				log.Printf("Failed to sync existing events for cluster %s: %v", listener.cluster, err)
			} else {
				listener.synced.Store(true)
			}
			rv = listRV
		}
		expired, err := el.streamEvents(ctx, listener, &rv)
		if expired {
			rv = ""
			continue
		}
		if err != nil {
			log.Printf("Event streaming error for cluster %s: %v, retrying in 10s", listener.cluster, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
	}
}

// syncExistingEvents lists all of the cluster's events and stores the newest
// windowSize of them. A LIST pages in namespace/name order: stopping once
// windowSize had been stored kept the alphabetically first namespaces of a
// busy cluster, and RecentEvents served their newest events rather than the
// cluster's.
func (el *EventListener) syncExistingEvents(ctx context.Context, listener *clusterListener) (string, error) {
	var rv string
	listed := 0
	var kept newestEvents
	var listErr error
	opts := metav1.ListOptions{Limit: 1000}
	for {
		eventList, err := listener.client.CoreV1().Events("").List(ctx, opts)
		if err != nil {
			listErr = fmt.Errorf("failed to list existing events: %v", err)
			break
		}
		if rv == "" {
			rv = eventList.ResourceVersion
		}
		for i := range eventList.Items {
			kept.keep(el.convertToDBEvent(&eventList.Items[i]), el.windowSize)
		}
		listed += len(eventList.Items)
		opts.Continue = eventList.Continue
		if opts.Continue == "" {
			break
		}
	}
	if el.db != nil {
		for i := 0; i < len(kept); i += 1000 {
			if err := el.db.StoreEvents(listener.cluster, kept[i:min(i+1000, len(kept))]); err != nil {
				return rv, fmt.Errorf("failed to store events: %v", err)
			}
		}
	}
	if listErr != nil {
		return rv, listErr
	}
	log.Printf("Synced %d of %d existing events for cluster %s (rv=%s)", len(kept), listed, listener.cluster, rv)
	return rv, nil
}

// newestEvents keeps the newest events offered to it, up to a limit: a
// min-heap on EventTime whose root is the oldest event kept.
type newestEvents []db.K8sEvent

func (h newestEvents) Len() int           { return len(h) }
func (h newestEvents) Less(i, j int) bool { return h[i].EventTime.Before(h[j].EventTime) }
func (h newestEvents) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *newestEvents) Push(x any)        { *h = append(*h, x.(db.K8sEvent)) }
func (h *newestEvents) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

func (h *newestEvents) keep(e db.K8sEvent, limit int) {
	if h.Len() < limit {
		heap.Push(h, e)
		return
	}
	if limit > 0 && e.EventTime.After((*h)[0].EventTime) {
		(*h)[0] = e
		heap.Fix(h, 0)
	}
}

func (el *EventListener) streamEvents(ctx context.Context, listener *clusterListener, rv *string) (expired bool, err error) {
	watcher, err := listener.client.CoreV1().Events("").Watch(ctx, metav1.ListOptions{ResourceVersion: *rv, AllowWatchBookmarks: true})
	if err != nil {
		if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
			return true, nil
		}
		return false, fmt.Errorf("failed to create event watcher: %v", err)
	}
	defer watcher.Stop()

	log.Printf("Started streaming events for cluster %s from rv=%s", listener.cluster, *rv)

	batchEvents := make([]db.K8sEvent, 0, 100)
	batchTicker := time.NewTicker(1 * time.Second)
	defer batchTicker.Stop()
	flush := func() {
		if len(batchEvents) > 0 {
			el.storeBatch(listener.cluster, batchEvents)
			batchEvents = batchEvents[:0]
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return false, nil

		case <-batchTicker.C:
			flush()

		case event, ok := <-watcher.ResultChan():
			if !ok {
				flush()
				return false, fmt.Errorf("watch channel closed")
			}

			switch event.Type {
			case watch.Error:
				flush()
				if status, ok := event.Object.(*metav1.Status); ok && (status.Code == 410 || status.Reason == metav1.StatusReasonExpired || status.Reason == metav1.StatusReasonGone) {
					return true, nil
				}
				return false, fmt.Errorf("watch error for cluster %s", listener.cluster)
			case watch.Bookmark:
				if m, ok := event.Object.(metav1.Object); ok {
					*rv = m.GetResourceVersion()
				}
			case watch.Added, watch.Modified:
				if k8sEvent, ok := event.Object.(*corev1.Event); ok {
					*rv = k8sEvent.ResourceVersion
					batchEvents = append(batchEvents, el.convertToDBEvent(k8sEvent))
					if len(batchEvents) >= 100 {
						flush()
					}
				}
			}
		}
	}
}

func (el *EventListener) storeBatch(cluster string, events []db.K8sEvent) {
	if el.db == nil {
		return
	}
	if err := el.db.StoreEvents(cluster, events); err != nil {
		log.Printf("Failed to store event batch for cluster %s: %v", cluster, err)
	}
}

func (el *EventListener) convertToDBEvent(event *corev1.Event) db.K8sEvent {
	dbEvent := db.K8sEvent{
		UID:                      string(event.UID),
		Name:                     event.Name,
		Namespace:                event.Namespace,
		InvolvedObjectUID:        string(event.InvolvedObject.UID),
		InvolvedObjectName:       event.InvolvedObject.Name,
		InvolvedObjectNamespace:  event.InvolvedObject.Namespace,
		InvolvedObjectKind:       event.InvolvedObject.Kind,
		InvolvedObjectAPIVersion: event.InvolvedObject.APIVersion,
		Type:                     event.Type,
		Reason:                   event.Reason,
		Message:                  event.Message,
		Count:                    event.Count,
		SourceComponent:          event.Source.Component,
		SourceHost:               event.Source.Host,
	}

	if !event.FirstTimestamp.IsZero() {
		dbEvent.FirstTimestamp = event.FirstTimestamp.Time
	}
	if !event.LastTimestamp.IsZero() {
		dbEvent.LastTimestamp = event.LastTimestamp.Time
	}
	if !event.EventTime.IsZero() {
		dbEvent.EventTime = event.EventTime.Time
	} else if !event.LastTimestamp.IsZero() {
		dbEvent.EventTime = event.LastTimestamp.Time
	} else {
		dbEvent.EventTime = time.Now()
	}

	return dbEvent
}

// StopListening stops the cluster's listener as its tab closes. The cluster
// stays released until it is started explicitly again.
func (el *EventListener) StopListening(cluster string) {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.released[cluster] = true
	if listener, exists := el.listeners[cluster]; exists {
		if listener.isActive {
			listener.cancel()
			listener.isActive = false
		}
		delete(el.listeners, cluster)
		log.Printf("Stopped event listener for cluster %s", cluster)
	}
}

func (el *EventListener) StopAll() {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.closed = true

	for cluster, listener := range el.listeners {
		if listener.isActive {
			listener.cancel()
			listener.isActive = false
			log.Printf("Stopped event listener for cluster %s", cluster)
		}
	}

	el.listeners = make(map[string]*clusterListener)
}
