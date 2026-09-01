package search

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/k8s/watcher"
	"github.com/kanivet/backend/internal/topics"
)

// WatcherBridge connects the k8s watcher service to the search service
type WatcherBridge struct {
	searchService *Service
	watcherSvc    *watcher.Service
}

// NewWatcherBridge creates a new bridge between k8s watcher and search service
func NewWatcherBridge(searchService *Service, watcherSvc *watcher.Service) *WatcherBridge {
	return &WatcherBridge{
		searchService: searchService,
		watcherSvc:    watcherSvc,
	}
}

// SearchBroadcaster wraps the search service to receive broadcast events
type SearchBroadcaster struct {
	bridge *WatcherBridge
}

// SetBridge sets the watcher bridge for the broadcaster
func (sb *SearchBroadcaster) SetBridge(bridge *WatcherBridge) {
	sb.bridge = bridge
}

func (sb *SearchBroadcaster) Broadcast(topic string, message watcher.Message) error {
	if !strings.HasPrefix(topic, "items:") {
		return nil
	}
	cluster, group, version, kind, namespace, ok := topics.ParseItemsTopic(topic)
	if !ok {
		return nil
	}
	switch m := message.(type) {
	case *watcher.ResourceEventMessage:
		if m == nil || m.Action == "" || m.Item == nil {
			return nil
		}
		sb.bridge.searchService.OnResourceEvent(cluster, group, version, kind, namespace, m.Action, m.Item)
	case *watcher.BulkListMessage:
		if m == nil || m.IsMinimal {
			return nil
		}
		for _, item := range m.Items {
			if item == nil {
				continue
			}
			sb.bridge.searchService.OnResourceEvent(cluster, group, version, kind, namespace, "ADDED", item)
		}
	}
	return nil
}

func (sb *SearchBroadcaster) BroadcastDirect(topic string, message watcher.Message) error {
	return sb.Broadcast(topic, message)
}

func (sb *SearchBroadcaster) BroadcastAll(message watcher.Message) error {
	return nil
}

func (sb *SearchBroadcaster) FlushTopic(topic string) error { return nil }

func (sb *SearchBroadcaster) SetSortPreference(topic, sortBy, sortOrder string) {}

func (sb *SearchBroadcaster) GetSortPreference(topic string) (string, string) { return "age", "desc" }

func (sb *SearchBroadcaster) CleanupTopic(topic string) {}

// StartWatchingCluster starts watching all resources immediately
// This ensures we get real-time updates while background sync is running
func (b *WatcherBridge) StartWatchingCluster(cluster string) error {
	log.Printf("Watcher bridge preparing to watch cluster %s", cluster)

	// First trigger the search service indexing notification
	if err := b.searchService.IndexCluster(cluster); err != nil {
		return err
	}

	// Gate wide watching behind env flag to avoid flooding large clusters by default
	if watchAllEnabled() {
		go b.startWatchersWithResourceVersions(cluster)
	}
	return nil
}

func watchAllEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("kanivet_WATCH_ALL")))
	return v == "1" || v == "true" || v == "yes"
}

// startWatchersWithResourceVersions starts watchers using stored resource versions
// This ensures we don't miss events between indexing and watching
func (b *WatcherBridge) startWatchersWithResourceVersions(cluster string) {
	// Small delay to let the cache load complete
	time.Sleep(2 * time.Second)

	log.Printf("Starting watchers for cluster %s with resource version tracking", cluster)

	// Discover API resources
	resources, err := b.searchService.ListAPIResources(cluster)
	if err != nil {
		log.Printf("Failed to list API resources for %s: %v", cluster, err)
		return
	}

	// Filter and prepare resources
	type res struct{ group, version, resource, kind string }
	var list []res

	// Priority resources to watch first
	priority := []res{}
	others := []res{}
	priorityMap := map[string]bool{
		"pods": true, "deployments": true, "services": true,
		"configmaps": true, "secrets": true, "jobs": true,
	}

	for _, r := range resources {
		if strings.Contains(r.Name, "/") {
			continue
		}
		// Skip non-watchable resources
		if !hasVerb(r.Verbs, "list") || !hasVerb(r.Verbs, "watch") {
			continue
		}
		// Skip events and component statuses
		if r.Kind == "Event" || r.Kind == "ComponentStatus" || r.Name == "events" {
			continue
		}

		resInfo := res{group: r.Group, version: r.Version, resource: r.Name, kind: r.Kind}
		if priorityMap[r.Name] {
			priority = append(priority, resInfo)
		} else {
			others = append(others, resInfo)
		}
	}

	// Watch priority resources first
	list = append(priority, others...)
	count := len(list)
	log.Printf("Starting watchers for %d resource types in cluster %s", count, cluster)

	// Start watchers with controlled concurrency
	sem := make(chan struct{}, 2) // Reduced concurrency for stability
	for _, it := range list {
		sem <- struct{}{}
		go func(g, v, r, k string) {
			defer func() { <-sem }()

			// Mark search service as interested in this resource
			_ = b.searchService.IndexResourceType(cluster, g, v, r)

			if err := b.watcherSvc.StartWatch(cluster, g, v, r, "", "age", "desc"); err != nil {
				if !strings.Contains(err.Error(), "already watching") {
					log.Printf("Failed to start watch for %s/%s in cluster %s: %v", g, r, cluster, err)
				}
			}

			// Small delay to avoid API overload
			time.Sleep(100 * time.Millisecond)
		}(it.group, it.version, it.resource, it.kind)
	}

	// Wait for all watchers to start
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}

	log.Printf("Watchers started for cluster %s with resource version tracking", cluster)
}

// StartAllWatchersForCluster starts all watchers for a cluster (exported for callback)
func (b *WatcherBridge) StartAllWatchersForCluster(cluster string) {
	b.startAllWatchers(cluster)
}

// startAllWatchers starts watchers for all discovered resources
func (b *WatcherBridge) startAllWatchers(cluster string) {
	// Discover API resources
	resources, err := b.searchService.ListAPIResources(cluster)
	if err != nil {
		log.Printf("Failed to list API resources for %s: %v", cluster, err)
		return
	}

	// Filter resources
	type res struct{ group, version, resource string }
	list := make([]res, 0, len(resources))
	for _, r := range resources {
		if strings.Contains(r.Name, "/") {
			continue
		}
		// Skip non-watchable resources
		if !hasVerb(r.Verbs, "list") || !hasVerb(r.Verbs, "watch") {
			continue
		}
		// Skip events and component statuses
		if r.Kind == "Event" || r.Kind == "ComponentStatus" || r.Name == "events" {
			continue
		}
		list = append(list, res{group: r.Group, version: r.Version, resource: r.Name})
	}

	log.Printf("Starting watchers for %d resource types in cluster %s", len(list), cluster)

	// Track how many watchers were started vs skipped
	started := 0
	skipped := 0

	// Start watchers with controlled concurrency and rate limiting
	sem := make(chan struct{}, 3) // Only 3 concurrent watcher starts
	for _, it := range list {
		sem <- struct{}{}
		go func(g, v, k string) {
			defer func() { <-sem }()

			// Small delay between watcher starts to avoid overwhelming the API
			time.Sleep(200 * time.Millisecond)

			// Mark search service as interested in this resource
			_ = b.searchService.IndexResourceType(cluster, g, v, k)

			if err := b.watcherSvc.StartWatch(cluster, g, v, k, "", "age", "desc"); err != nil {
				if strings.Contains(err.Error(), "already watching") {
					skipped++
				} else {
					log.Printf("Failed to start watch for %s/%s in cluster %s: %v", g, k, cluster, err)
				}
			} else {
				started++
			}
		}(it.group, it.version, it.resource)
	}

	// Wait for all watchers to start
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}

	log.Printf("Watchers for cluster %s: %d started, %d already running", cluster, started, skipped)
}

func (b *WatcherBridge) StartWatchingResourceType(cluster, group, version, kind string) error {
	return b.watcherSvc.StartWatch(cluster, group, version, kind, "", "age", "desc")
}

// StopWatchingCluster stops watching all resources in a cluster
func (b *WatcherBridge) StopWatchingCluster(cluster string) {
	// This would need to track which watches were started per cluster
	// For now, we'll rely on the watcher service's internal management
	log.Printf("Stop watching cluster %s requested - watches will timeout naturally", cluster)
}
