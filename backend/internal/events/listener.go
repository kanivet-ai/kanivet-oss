package events

import (
	"context"
	"fmt"
	"log"
	"sync"
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
}

type clusterListener struct {
	cluster  string
	client   kubernetes.Interface
	cancel   context.CancelFunc
	isActive bool
	lastSync time.Time
}

func NewEventListener(k8sClient k8s.Interface, database *db.DB) *EventListener {
	return &EventListener{
		k8sClient:  k8sClient,
		db:         database,
		listeners:  make(map[string]*clusterListener),
		windowSize: 10000,
	}
}

func (el *EventListener) StartListening(cluster string) error {
	el.mu.Lock()
	defer el.mu.Unlock()

	if listener, exists := el.listeners[cluster]; exists && listener.isActive {
		log.Printf("Event listener for cluster %s is already active", cluster)
		return nil
	}

	client, err := el.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return fmt.Errorf("failed to get client for cluster %s: %v", cluster, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	listener := &clusterListener{
		cluster:  cluster,
		client:   client,
		cancel:   cancel,
		isActive: true,
		lastSync: time.Now(),
	}

	el.listeners[cluster] = listener

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

func (el *EventListener) syncExistingEvents(ctx context.Context, listener *clusterListener) (string, error) {
	var rv string
	total := 0
	opts := metav1.ListOptions{Limit: 1000}
	for {
		eventList, err := listener.client.CoreV1().Events("").List(ctx, opts)
		if err != nil {
			return rv, fmt.Errorf("failed to list existing events: %v", err)
		}
		if rv == "" {
			rv = eventList.ResourceVersion
		}
		if len(eventList.Items) > 0 {
			events := make([]db.K8sEvent, 0, len(eventList.Items))
			for i := range eventList.Items {
				events = append(events, el.convertToDBEvent(&eventList.Items[i]))
			}
			if el.db != nil {
				if err := el.db.StoreEvents(listener.cluster, events); err != nil {
					return rv, fmt.Errorf("failed to store events: %v", err)
				}
			}
			total += len(events)
		}
		opts.Continue = eventList.Continue
		if opts.Continue == "" || total >= el.windowSize {
			break
		}
	}
	log.Printf("Synced %d existing events for cluster %s (rv=%s)", total, listener.cluster, rv)
	return rv, nil
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

func (el *EventListener) StopListening(cluster string) {
	el.mu.Lock()
	defer el.mu.Unlock()
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

	for cluster, listener := range el.listeners {
		if listener.isActive {
			listener.cancel()
			listener.isActive = false
			log.Printf("Stopped event listener for cluster %s", cluster)
		}
	}

	el.listeners = make(map[string]*clusterListener)
}
