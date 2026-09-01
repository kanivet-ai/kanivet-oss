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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
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

func (el *EventListener) watchEvents(ctx context.Context, listener *clusterListener) {
	log.Printf("Starting event watcher for cluster %s", listener.cluster)

	didInitialSync := false
	for {
		select {
		case <-ctx.Done():
			log.Printf("Event watcher context cancelled for cluster %s", listener.cluster)
			return
		default:
			if !didInitialSync {
				if err := el.syncExistingEvents(listener); err != nil {
					log.Printf("Failed to sync existing events for cluster %s: %v", listener.cluster, err)
				}
				didInitialSync = true
			}

			if err := el.streamEvents(ctx, listener); err != nil {
				log.Printf("Event streaming error for cluster %s: %v, retrying in 10s", listener.cluster, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
					continue
				}
			}
		}
	}
}

func (el *EventListener) syncExistingEvents(listener *clusterListener) error {
	eventList, err := listener.client.CoreV1().Events("").List(context.Background(), metav1.ListOptions{
		Limit: 1000,
	})
	if err != nil {
		return fmt.Errorf("failed to list existing events: %v", err)
	}

	if len(eventList.Items) == 0 {
		return nil
	}

	events := make([]db.K8sEvent, 0, len(eventList.Items))
	for _, event := range eventList.Items {
		events = append(events, el.convertToDBEvent(&event))
	}

	if el.db != nil {
		if err := el.db.StoreEvents(listener.cluster, events); err != nil {
			return fmt.Errorf("failed to store events: %v", err)
		}
	}

	log.Printf("Synced %d existing events for cluster %s", len(events), listener.cluster)
	return nil
}

func (el *EventListener) streamEvents(ctx context.Context, listener *clusterListener) error {
	fieldSelector := fields.Everything()
	listOptions := metav1.ListOptions{
		FieldSelector: fieldSelector.String(),
		Watch:         true,
	}

	watcher, err := listener.client.CoreV1().Events("").Watch(ctx, listOptions)
	if err != nil {
		return fmt.Errorf("failed to create event watcher: %v", err)
	}
	defer watcher.Stop()

	log.Printf("Started streaming events for cluster %s", listener.cluster)

	batchEvents := make([]db.K8sEvent, 0, 100)
	batchTicker := time.NewTicker(1 * time.Second)
	defer batchTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			if len(batchEvents) > 0 {
				el.storeBatch(listener.cluster, batchEvents)
			}
			return nil

		case <-batchTicker.C:
			if len(batchEvents) > 0 {
				el.storeBatch(listener.cluster, batchEvents)
				batchEvents = batchEvents[:0]
			}

		case event, ok := <-watcher.ResultChan():
			if !ok {
				if len(batchEvents) > 0 {
					el.storeBatch(listener.cluster, batchEvents)
				}
				return fmt.Errorf("watch channel closed")
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				if k8sEvent, ok := event.Object.(*corev1.Event); ok {
					batchEvents = append(batchEvents, el.convertToDBEvent(k8sEvent))

					if len(batchEvents) >= 100 {
						el.storeBatch(listener.cluster, batchEvents)
						batchEvents = batchEvents[:0]
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
