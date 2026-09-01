package handlers

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/kanivet/backend/internal/helm"
	"github.com/kanivet/backend/internal/websocket/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type HelmHandler struct {
	helmService *helm.Service
	hub         *core.Hub
	mu          sync.RWMutex
	streams     map[string]*helmStream
}

type helmStream struct {
	cluster    string
	cancelFunc context.CancelFunc
	stopChan   chan struct{}
}

type HelmReleasesMessage struct {
	core.BaseMessage
	Cluster   string         `json:"cluster"`
	Releases  []helm.Release `json:"releases"`
	Namespace string         `json:"namespace,omitempty"` // Which namespace these came from
	Done      bool           `json:"done"`                // True when all namespaces are loaded
	Total     int            `json:"total,omitempty"`     // Total releases so far
	Progress  *LoadProgress  `json:"progress,omitempty"`  // Loading progress
}

type HelmReleaseEvent struct {
	core.BaseMessage
	Cluster   string       `json:"cluster"`
	EventType string       `json:"eventType"` // "added", "modified", "deleted"
	Release   helm.Release `json:"release"`
}

type LoadProgress struct {
	NamespacesTotal     int `json:"namespacesTotal"`
	NamespacesCompleted int `json:"namespacesCompleted"`
}

func (m *HelmReleasesMessage) Marshal() ([]byte, error) {
	return sonic.Marshal(m)
}

func (m *HelmReleaseEvent) Marshal() ([]byte, error) {
	return sonic.Marshal(m)
}

func NewHelmHandler(helmService *helm.Service, hub *core.Hub) *HelmHandler {
	return &HelmHandler{
		helmService: helmService,
		hub:         hub,
		streams:     make(map[string]*helmStream),
	}
}

func (h *HelmHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"helm"}
}

func (h *HelmHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var payload struct {
		Action  string `json:"action"`
		Cluster string `json:"cluster"`
	}

	if err := sonic.Unmarshal(msg.Payload, &payload); err != nil {
		return err
	}

	switch payload.Action {
	case "subscribe":
		return h.handleSubscribe(ctx, conn, payload.Cluster)
	case "unsubscribe":
		return h.handleUnsubscribe(conn, payload.Cluster)
	default:
		return nil
	}
}

func (h *HelmHandler) handleSubscribe(ctx context.Context, conn *core.Connection, cluster string) error {
	topic := "helm:releases:" + cluster

	// Subscribe connection to topic
	if _, err := h.hub.Subscribe(topic, conn); err != nil {
		return err
	}

	// Start streaming if not already running
	h.mu.Lock()
	if _, exists := h.streams[cluster]; !exists {
		streamCtx, cancel := context.WithCancel(context.Background())
		stopChan := make(chan struct{})

		h.streams[cluster] = &helmStream{
			cluster:    cluster,
			cancelFunc: cancel,
			stopChan:   stopChan,
		}

		go h.streamReleases(streamCtx, cluster, topic, stopChan)
		log.Printf("[HelmHandler] Started streaming for cluster: %s", cluster)
	}
	h.mu.Unlock()

	return nil
}

func (h *HelmHandler) handleUnsubscribe(conn *core.Connection, cluster string) error {
	topic := "helm:releases:" + cluster

	if _, err := h.hub.Unsubscribe(topic, conn); err != nil {
		return err
	}

	// Check if we should stop streaming
	subscribers := h.hub.Subscribers(topic)
	if len(subscribers) == 0 {
		h.mu.Lock()
		if stream, exists := h.streams[cluster]; exists {
			stream.cancelFunc()
			close(stream.stopChan)
			delete(h.streams, cluster)
			log.Printf("[HelmHandler] Stopped streaming for cluster: %s", cluster)
		}
		h.mu.Unlock()
	}

	return nil
}

func (h *HelmHandler) streamReleases(ctx context.Context, cluster, topic string, stopChan chan struct{}) {
	// Stream releases using the new streaming API
	releasesChan, progressChan, err := h.helmService.StreamReleases(ctx, cluster)
	if err != nil {
		log.Printf("[HelmHandler] Error starting stream for %s: %v", cluster, err)
		return
	}

	var allReleases []helm.Release
	var lastProgress *LoadProgress

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopChan:
			return
		case progress, ok := <-progressChan:
			if !ok {
				progressChan = nil
				continue
			}
			lastProgress = &LoadProgress{
				NamespacesTotal:     progress.Total,
				NamespacesCompleted: progress.Completed,
			}
		case releases, ok := <-releasesChan:
			if !ok {
				// Channel closed - send final message and start watching
				msg := &HelmReleasesMessage{
					BaseMessage: core.BaseMessage{
						MessageType: "helm",
						Timestamp:   time.Now(),
					},
					Cluster:  cluster,
					Releases: allReleases,
					Done:     true,
					Total:    len(allReleases),
					Progress: lastProgress,
				}
				if err := h.hub.Broadcast(topic, msg); err != nil {
					log.Printf("[HelmHandler] Error broadcasting final: %v", err)
				}
				
				// Start watching for changes after initial load
				go h.watchHelmSecrets(ctx, cluster, topic, stopChan)
				return
			}

			// Accumulate and broadcast incremental results
			allReleases = append(allReleases, releases...)

			msg := &HelmReleasesMessage{
				BaseMessage: core.BaseMessage{
					MessageType: "helm",
					Timestamp:   time.Now(),
				},
				Cluster:  cluster,
				Releases: releases, // Just the new releases
				Done:     false,
				Total:    len(allReleases),
				Progress: lastProgress,
			}

			if err := h.hub.Broadcast(topic, msg); err != nil {
				log.Printf("[HelmHandler] Error broadcasting: %v", err)
			}
		}
	}
}

// watchHelmSecrets watches for changes to Helm release secrets and broadcasts updates
func (h *HelmHandler) watchHelmSecrets(ctx context.Context, cluster, topic string, stopChan chan struct{}) {
	log.Printf("[HelmHandler] Starting Helm secrets watch for cluster: %s", cluster)

	k8sClient, _, err := h.helmService.GetK8sClient(cluster)
	if err != nil {
		log.Printf("[HelmHandler] Failed to get k8s client for watch: %v", err)
		return
	}

	// Watch secrets with owner=helm label (this is how Helm stores releases)
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopChan:
			return
		default:
		}

		// First list to get the current ResourceVersion
		// This ensures we only get NEW events, not replays of existing secrets
		secrets, err := k8sClient.CoreV1().Secrets("").List(ctx, metav1.ListOptions{
			LabelSelector: "owner=helm",
		})
		if err != nil {
			log.Printf("[HelmHandler] Failed to list secrets for watch: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		resourceVersion := secrets.ResourceVersion
		log.Printf("[HelmHandler] Starting watch from ResourceVersion: %s", resourceVersion)

		watcher, err := k8sClient.CoreV1().Secrets("").Watch(ctx, metav1.ListOptions{
			LabelSelector:   "owner=helm",
			ResourceVersion: resourceVersion,
		})
		if err != nil {
			log.Printf("[HelmHandler] Failed to start watch: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		h.processWatchEvents(ctx, watcher, cluster, topic, stopChan)
		
		// If we get here, watch ended - restart after a short delay
		select {
		case <-ctx.Done():
			return
		case <-stopChan:
			return
		case <-time.After(time.Second):
			log.Printf("[HelmHandler] Restarting watch for cluster: %s", cluster)
		}
	}
}

func (h *HelmHandler) processWatchEvents(ctx context.Context, watcher watch.Interface, cluster, topic string, stopChan chan struct{}) {
	defer watcher.Stop()

	// Track last known status for each release to detect real changes
	lastStatus := make(map[string]string)
	// Debounce map to avoid multiple fetches for the same release
	lastFetch := make(map[string]time.Time)
	debounceInterval := 500 * time.Millisecond // Reduced debounce for faster updates

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopChan:
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watch channel closed
				return
			}

			// Parse the secret to get basic release info (name, namespace)
			basicRelease, err := h.helmService.ParseReleaseFromSecret(event.Object)
			if err != nil {
				// Not a valid helm release secret or parsing error
				continue
			}

			var eventType string
			switch event.Type {
			case watch.Added:
				eventType = "added"
			case watch.Modified:
				eventType = "modified"
			case watch.Deleted:
				eventType = "deleted"
			default:
				continue
			}

			releaseKey := basicRelease.Namespace + "/" + basicRelease.Name
			
			// For delete events, just send the basic info
			if eventType == "deleted" {
				log.Printf("[HelmHandler] Helm release deleted: %s", releaseKey)
				delete(lastStatus, releaseKey)
				msg := &HelmReleaseEvent{
					BaseMessage: core.BaseMessage{
						MessageType: "helm",
						Timestamp:   time.Now(),
					},
					Cluster:   cluster,
					EventType: eventType,
					Release:   *basicRelease,
				}
				if err := h.hub.Broadcast(topic, msg); err != nil {
					log.Printf("[HelmHandler] Error broadcasting delete event: %v", err)
				}
				continue
			}

			// Check if status changed - always process status changes immediately
			statusChanged := false
			if prev, ok := lastStatus[releaseKey]; ok {
				if basicRelease.Status != "" && basicRelease.Status != prev {
					statusChanged = true
					log.Printf("[HelmHandler] Status changed for %s: %s -> %s", releaseKey, prev, basicRelease.Status)
				}
			}
			if basicRelease.Status != "" {
				lastStatus[releaseKey] = basicRelease.Status
			}

			// For add/modify events, debounce (but skip debounce for status changes)
			if !statusChanged {
				if lastTime, ok := lastFetch[releaseKey]; ok {
					if time.Since(lastTime) < debounceInterval {
						continue // Skip this event, too soon
					}
				}
			}
			lastFetch[releaseKey] = time.Now()

			// Fetch full release info from Helm with retry
			go func(ns, name, evtType string) {
				var fullRelease *helm.Release
				var err error
				
				// Retry up to 3 times with increasing delay
				// Helm might not have fully registered the release yet
				for attempt := 0; attempt < 3; attempt++ {
					if attempt > 0 {
						time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
					}
					
					fullRelease, err = h.helmService.GetReleaseBasic(ctx, cluster, ns, name)
					if err != nil {
						log.Printf("[HelmHandler] Attempt %d: Failed to fetch release %s/%s: %v", attempt+1, ns, name, err)
						continue
					}
					
					// Check if we got complete data
					if fullRelease.Chart != "" && fullRelease.Status != "" {
						break // Success
					}
					
					log.Printf("[HelmHandler] Attempt %d: Got incomplete data for %s/%s, retrying...", attempt+1, ns, name)
				}
				
				if fullRelease == nil || fullRelease.Chart == "" {
					log.Printf("[HelmHandler] Failed to get complete release data for %s/%s after retries", ns, name)
					return
				}

				log.Printf("[HelmHandler] Helm release %s: %s/%s (chart=%s, status=%s, rev=%d)", 
					evtType, ns, name, fullRelease.Chart, fullRelease.Status, fullRelease.Revision)

				msg := &HelmReleaseEvent{
					BaseMessage: core.BaseMessage{
						MessageType: "helm",
						Timestamp:   time.Now(),
					},
					Cluster:   cluster,
					EventType: evtType,
					Release:   *fullRelease,
				}

				if err := h.hub.Broadcast(topic, msg); err != nil {
					log.Printf("[HelmHandler] Error broadcasting event: %v", err)
				}
			}(basicRelease.Namespace, basicRelease.Name, eventType)
		}
	}
}
