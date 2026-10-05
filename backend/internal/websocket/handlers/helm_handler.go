package handlers

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/helm"
	"github.com/kanivet/backend/internal/websocket/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	helmMinBackoff = time.Second
	helmMaxBackoff = 30 * time.Second
	// helmChunkBytes bounds the estimated size of one releases frame so a
	// snapshot never approaches the websocket message limit.
	helmChunkBytes = 256 * 1024
	// helmRVListTimeout bounds the one-item list used to get a resourceVersion.
	helmRVListTimeout = 30 * time.Second
)

// helmBackend is the part of helm.Service the handler uses.
type helmBackend interface {
	StreamReleases(ctx context.Context, cluster string) (<-chan []helm.Release, <-chan helm.StreamProgress, error)
	GetK8sClient(cluster string) (kubernetes.Interface, *rest.Config, error)
	ParseReleaseFromSecret(obj interface{}) (*helm.Release, error)
	GetReleaseBasic(ctx context.Context, cluster, namespace, name string) (*helm.Release, error)
}

type HelmHandler struct {
	helmService helmBackend
	hub         *core.Hub
	mu          sync.RWMutex
	streams     map[string]*helmStream
}

// helmStream is one list-and-watch loop per cluster, shared by every
// subscribed connection. It keeps a snapshot so late subscribers get the
// current releases instead of an empty list.
type helmStream struct {
	cluster    string
	topic      string
	cancelFunc context.CancelFunc

	subs map[core.ConnectionID]struct{} // guarded by HelmHandler.mu

	mu       sync.Mutex // guards the fields below
	releases map[string]helm.Release
	loaded   bool
	progress *LoadProgress
}

type HelmReleasesMessage struct {
	core.BaseMessage
	Cluster   string         `json:"cluster"`
	Releases  []helm.Release `json:"releases"`
	Namespace string         `json:"namespace,omitempty"` // Which namespace these came from
	Done      bool           `json:"done"`                // True when all namespaces are loaded
	Total     int            `json:"total,omitempty"`     // Total releases so far
	Progress  *LoadProgress  `json:"progress,omitempty"`  // Loading progress
	Error     string         `json:"error,omitempty"`     // Set when the initial load failed
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
	return jsonv2.Marshal(m)
}

func (m *HelmReleaseEvent) Marshal() ([]byte, error) {
	return jsonv2.Marshal(m)
}

func NewHelmHandler(helmService *helm.Service, hub *core.Hub) *HelmHandler {
	return newHelmHandler(helmService, hub)
}

func newHelmHandler(helmService helmBackend, hub *core.Hub) *HelmHandler {
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

	if err := jsonv2.Unmarshal(msg.Payload, &payload); err != nil {
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

// OnConnectionClose drops the closed connection from every stream and stops
// the streams nobody listens to any more.
func (h *HelmHandler) OnConnectionClose(conn *core.Connection) {
	h.mu.Lock()
	var stopped []*helmStream
	for cluster, st := range h.streams {
		if _, ok := st.subs[conn.ID()]; !ok {
			continue
		}
		delete(st.subs, conn.ID())
		if len(st.subs) == 0 {
			delete(h.streams, cluster)
			stopped = append(stopped, st)
		}
	}
	h.mu.Unlock()

	for _, st := range stopped {
		st.cancelFunc()
		log.Printf("[HelmHandler] Stopped streaming for cluster: %s (last subscriber disconnected)", st.cluster)
	}
}

// HasActiveStream reports whether a stream runs for the cluster.
func (h *HelmHandler) HasActiveStream(cluster string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.streams[cluster]
	return ok
}

func (h *HelmHandler) handleSubscribe(ctx context.Context, conn *core.Connection, cluster string) error {
	topic := "helm:releases:" + cluster

	// Subscribe connection to topic (idempotent per connection)
	if _, err := h.hub.Subscribe(topic, conn); err != nil {
		return err
	}

	h.mu.Lock()
	st, exists := h.streams[cluster]
	if !exists {
		streamCtx, cancel := context.WithCancel(context.Background())
		st = &helmStream{
			cluster:    cluster,
			topic:      topic,
			cancelFunc: cancel,
			subs:       make(map[core.ConnectionID]struct{}),
			releases:   make(map[string]helm.Release),
		}
		h.streams[cluster] = st
		go h.runStream(streamCtx, st)
		log.Printf("[HelmHandler] Started streaming for cluster: %s", cluster)
	}
	st.subs[conn.ID()] = struct{}{}
	h.mu.Unlock()

	// A new (or re-subscribing) connection gets the current state. For a
	// stream that just started this is empty and the live chunks follow.
	if exists {
		h.sendSnapshot(st, conn)
	}
	return nil
}

func (h *HelmHandler) handleUnsubscribe(conn *core.Connection, cluster string) error {
	topic := "helm:releases:" + cluster

	if _, err := h.hub.Unsubscribe(topic, conn); err != nil {
		return err
	}

	h.mu.Lock()
	var stopped *helmStream
	if st, exists := h.streams[cluster]; exists {
		delete(st.subs, conn.ID())
		if len(st.subs) == 0 {
			delete(h.streams, cluster)
			stopped = st
		}
	}
	h.mu.Unlock()

	if stopped != nil {
		stopped.cancelFunc()
		log.Printf("[HelmHandler] Stopped streaming for cluster: %s", cluster)
	}
	return nil
}

func releaseKey(r *helm.Release) string { return r.Namespace + "/" + r.Name }

// chunkReleases splits releases into groups whose estimated JSON size stays
// under helmChunkBytes.
func chunkReleases(releases []helm.Release) [][]helm.Release {
	var chunks [][]helm.Release
	var cur []helm.Release
	size := 0
	for _, r := range releases {
		n := 256
		if b, err := jsonv2.Marshal(r); err == nil {
			n = len(b)
		}
		if len(cur) > 0 && size+n > helmChunkBytes {
			chunks = append(chunks, cur)
			cur, size = nil, 0
		}
		cur = append(cur, r)
		size += n
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func (st *helmStream) sortedReleasesLocked() []helm.Release {
	out := make([]helm.Release, 0, len(st.releases))
	for _, r := range st.releases {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b helm.Release) int {
		if a.Namespace != b.Namespace {
			if a.Namespace < b.Namespace {
				return -1
			}
			return 1
		}
		if a.Name < b.Name {
			return -1
		} else if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return out
}

// newReleasesMsgLocked builds a frame; the caller holds st.mu.
func (h *HelmHandler) newReleasesMsgLocked(st *helmStream, releases []helm.Release, done bool) *HelmReleasesMessage {
	if releases == nil {
		releases = []helm.Release{}
	}
	return &HelmReleasesMessage{
		BaseMessage: core.BaseMessage{MessageType: "helm", Timestamp: time.Now()},
		Cluster:     st.cluster,
		Releases:    releases,
		Done:        done,
		Total:       len(st.releases),
		Progress:    st.progress,
	}
}

// sendSnapshot sends the stream's current releases to one connection,
// followed by the Done flag once the initial load finished.
func (h *HelmHandler) sendSnapshot(st *helmStream, conn *core.Connection) {
	st.mu.Lock()
	defer st.mu.Unlock()

	chunks := chunkReleases(st.sortedReleasesLocked())
	for _, c := range chunks {
		h.sendTo(conn, h.newReleasesMsgLocked(st, c, false))
	}
	if st.loaded {
		h.sendTo(conn, h.newReleasesMsgLocked(st, nil, true))
	} else if len(chunks) == 0 && st.progress != nil {
		h.sendTo(conn, h.newReleasesMsgLocked(st, nil, false))
	}
}

func (h *HelmHandler) sendTo(conn *core.Connection, msg core.Message) {
	data, err := msg.Marshal()
	if err != nil {
		log.Printf("[HelmHandler] Error marshaling snapshot: %v", err)
		return
	}
	if err := conn.Send(data); err != nil {
		log.Printf("[HelmHandler] Error sending snapshot to %s: %v", conn.ID(), err)
	}
}

// broadcastChunks upserts releases into the snapshot and broadcasts them in
// bounded frames.
func (h *HelmHandler) broadcastChunks(st *helmStream, releases []helm.Release) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range releases {
		st.releases[releaseKey(&releases[i])] = releases[i]
	}
	for _, c := range chunkReleases(releases) {
		if err := h.hub.Broadcast(st.topic, h.newReleasesMsgLocked(st, c, false)); err != nil {
			log.Printf("[HelmHandler] Error broadcasting: %v", err)
		}
	}
}

// publishEvent applies a watch event to the snapshot and broadcasts it.
func (h *HelmHandler) publishEvent(st *helmStream, eventType string, rel helm.Release) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if eventType == "deleted" {
		delete(st.releases, releaseKey(&rel))
	} else {
		st.releases[releaseKey(&rel)] = rel
	}
	msg := &HelmReleaseEvent{
		BaseMessage: core.BaseMessage{MessageType: "helm", Timestamp: time.Now()},
		Cluster:     st.cluster,
		EventType:   eventType,
		Release:     rel,
	}
	if err := h.hub.Broadcast(st.topic, msg); err != nil {
		log.Printf("[HelmHandler] Error broadcasting %s event: %v", eventType, err)
	}
}

// dropStream removes the stream from the map (if it is still the current
// one) and cancels its context.
func (h *HelmHandler) dropStream(st *helmStream) {
	h.mu.Lock()
	if h.streams[st.cluster] == st {
		delete(h.streams, st.cluster)
	}
	h.mu.Unlock()
	st.cancelFunc()
}

// sleepCtx waits for d or until ctx is done, reporting whether ctx is alive.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(d, maxD time.Duration) time.Duration {
	d *= 2
	if d > maxD {
		return maxD
	}
	return d
}

// runStream loads the releases, then keeps them current from a watch. A
// watch that ends cleanly resumes from the last seen resourceVersion; when
// that is not possible (410 Gone) or the load failed, the releases are
// listed again so the clients catch up on whatever happened in the gap.
func (h *HelmHandler) runStream(ctx context.Context, st *helmStream) {
	// Never leave a dead stream in the map: later subscribers must be able
	// to start a fresh one.
	defer h.dropStream(st)

	backoff := helmMinBackoff
	first := true
	needSnapshot := true
	rv := ""

	for ctx.Err() == nil {
		client, _, err := h.helmService.GetK8sClient(st.cluster)
		if err != nil {
			log.Printf("[HelmHandler] Failed to get k8s client for %s: %v", st.cluster, err)
			if first {
				h.failStream(st, err)
				return
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, helmMaxBackoff)
			continue
		}

		if needSnapshot {
			// Take the resourceVersion before listing so that events during
			// the list are replayed by the watch (upserts are idempotent).
			newRV, rvErr := h.currentResourceVersion(ctx, client)
			if rvErr != nil {
				log.Printf("[HelmHandler] Failed to get resourceVersion for %s: %v", st.cluster, rvErr)
			}
			if err := h.loadSnapshot(ctx, st, first); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[HelmHandler] Error loading releases for %s: %v", st.cluster, err)
				if first {
					h.failStream(st, err)
					return
				}
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff, helmMaxBackoff)
				continue
			}
			first = false
			if rvErr != nil {
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff, helmMaxBackoff)
				continue
			}
			rv = newRV
			needSnapshot = false
		}

		startedAt := time.Now()
		lastRV, relist, err := h.watchOnce(ctx, st, client, rv)
		if ctx.Err() != nil {
			return
		}
		if lastRV != "" {
			rv = lastRV
		}
		if relist {
			needSnapshot = true
		}
		if err != nil {
			log.Printf("[HelmHandler] Helm watch for %s ended: %v", st.cluster, err)
		}

		wait := time.Second
		if err != nil || time.Since(startedAt) < 30*time.Second {
			wait = backoff
			backoff = nextBackoff(backoff, helmMaxBackoff)
		} else {
			backoff = helmMinBackoff
		}
		if !sleepCtx(ctx, wait) {
			return
		}
		log.Printf("[HelmHandler] Restarting watch for cluster: %s", st.cluster)
	}
}

func (h *HelmHandler) failStream(st *helmStream, err error) {
	st.mu.Lock()
	msg := h.newReleasesMsgLocked(st, nil, true)
	msg.Error = fmt.Sprintf("failed to load helm releases: %v", err)
	berr := h.hub.Broadcast(st.topic, msg)
	st.mu.Unlock()
	if berr != nil {
		log.Printf("[HelmHandler] Error broadcasting failure: %v", berr)
	}
}

func (h *HelmHandler) currentResourceVersion(ctx context.Context, client kubernetes.Interface) (string, error) {
	listCtx, cancel := context.WithTimeout(ctx, helmRVListTimeout)
	defer cancel()
	// One item is enough: the list's resourceVersion is the same, and the
	// full list carried every revision of every release.
	secrets, err := client.CoreV1().Secrets("").List(listCtx, metav1.ListOptions{
		LabelSelector: "owner=helm",
		Limit:         1,
	})
	if err != nil {
		return "", err
	}
	return secrets.ResourceVersion, nil
}

// loadSnapshot lists every release. The first load streams chunks to the
// subscribers as they arrive; later loads send the whole result once it is
// complete. Both finish with a separate Done frame.
func (h *HelmHandler) loadSnapshot(ctx context.Context, st *helmStream, first bool) error {
	releasesChan, progressChan, err := h.helmService.StreamReleases(ctx, st.cluster)
	if err != nil {
		return err
	}

	var collected []helm.Release
	for releasesChan != nil || progressChan != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case progress, ok := <-progressChan:
			if !ok {
				progressChan = nil
				continue
			}
			st.mu.Lock()
			st.progress = &LoadProgress{
				NamespacesTotal:     progress.Total,
				NamespacesCompleted: progress.Completed,
			}
			st.mu.Unlock()
		case releases, ok := <-releasesChan:
			if !ok {
				releasesChan = nil
				continue
			}
			if first {
				h.broadcastChunks(st, releases)
			} else {
				collected = append(collected, releases...)
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if !first {
		h.broadcastChunks(st, collected)
	}
	st.mu.Lock()
	st.loaded = true
	err = h.hub.Broadcast(st.topic, h.newReleasesMsgLocked(st, nil, true))
	st.mu.Unlock()
	if err != nil {
		log.Printf("[HelmHandler] Error broadcasting final: %v", err)
	}
	return nil
}

// watchOnce runs one watch from rv. It returns the last resourceVersion it
// saw and whether the releases must be listed again (resourceVersion too old).
func (h *HelmHandler) watchOnce(ctx context.Context, st *helmStream, client kubernetes.Interface, rv string) (lastRV string, relist bool, err error) {
	log.Printf("[HelmHandler] Starting watch for %s from ResourceVersion: %s", st.cluster, rv)
	watcher, err := client.CoreV1().Secrets("").Watch(ctx, metav1.ListOptions{
		LabelSelector:   "owner=helm",
		ResourceVersion: rv,
	})
	if err != nil {
		if apierrors.IsGone(err) || apierrors.IsResourceExpired(err) {
			return "", true, nil
		}
		return "", false, err
	}
	lastRV, relist = h.processWatchEvents(ctx, watcher, st)
	return lastRV, relist, nil
}

// processWatchEvents handles events until the watch ends. It returns the
// last resourceVersion seen and whether a relist is needed.
func (h *HelmHandler) processWatchEvents(ctx context.Context, watcher watch.Interface, st *helmStream) (lastRV string, relist bool) {
	defer watcher.Stop()
	cluster := st.cluster

	// Track last known status for each release to detect real changes
	lastStatus := make(map[string]string)
	// Debounce map to avoid multiple fetches for the same release
	lastFetch := make(map[string]time.Time)
	debounceInterval := 500 * time.Millisecond // Reduced debounce for faster updates

	for {
		select {
		case <-ctx.Done():
			return lastRV, false
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watch channel closed
				return lastRV, false
			}

			if event.Type == watch.Error {
				if status, ok := event.Object.(*metav1.Status); ok && (status.Code == 410 || status.Reason == metav1.StatusReasonExpired || status.Reason == metav1.StatusReasonGone) {
					return lastRV, true
				}
				return lastRV, false
			}
			if acc, err := meta.Accessor(event.Object); err == nil && acc.GetResourceVersion() != "" {
				lastRV = acc.GetResourceVersion()
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

			key := releaseKey(basicRelease)

			// For delete events, just send the basic info
			if eventType == "deleted" {
				log.Printf("[HelmHandler] Helm release deleted: %s", key)
				delete(lastStatus, key)
				h.publishEvent(st, eventType, *basicRelease)
				continue
			}

			// Check if status changed - always process status changes immediately
			statusChanged := false
			if prev, ok := lastStatus[key]; ok {
				if basicRelease.Status != "" && basicRelease.Status != prev {
					statusChanged = true
					log.Printf("[HelmHandler] Status changed for %s: %s -> %s", key, prev, basicRelease.Status)
				}
			}
			if basicRelease.Status != "" {
				lastStatus[key] = basicRelease.Status
			}

			// For add/modify events, debounce (but skip debounce for status changes)
			if !statusChanged {
				if lastTime, ok := lastFetch[key]; ok {
					if time.Since(lastTime) < debounceInterval {
						continue // Skip this event, too soon
					}
				}
			}
			lastFetch[key] = time.Now()

			// Fetch full release info from Helm with retry
			go func(ns, name, evtType string) {
				var fullRelease *helm.Release
				var err error

				// Retry up to 3 times with increasing delay
				// Helm might not have fully registered the release yet
				for attempt := 0; attempt < 3; attempt++ {
					if attempt > 0 {
						if !sleepCtx(ctx, time.Duration(attempt)*500*time.Millisecond) {
							return
						}
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

				h.publishEvent(st, evtType, *fullRelease)
			}(basicRelease.Namespace, basicRelease.Name, eventType)
		}
	}
}
