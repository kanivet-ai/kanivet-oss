package search

import (
	"hash/fnv"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/search/storage"
	"github.com/kanivet/backend/internal/utils"
)

// resourceCoords are the coordinates of the items topic an event arrived on:
// the group, version and plural resource name the watch was started with,
// plus the Kind discovery reported for that resource when known. They are
// authoritative for the document ID; the object's own kind/apiVersion fill in
// only what the topic does not say.
type resourceCoords struct {
	group, version, resource, kind string
}

type ResourceEventHandler struct {
	index       *storage.ShardedIndex
	db          *db.DB
	batchWriter *BatchWriter

	// fingerprints tracks the last seen "searchable subset" hash per resource ID.
	// On a MODIFIED event whose searchable fields haven't changed (e.g. only
	// status.containerStatuses ticking) we skip the index/persist work.
	fpMu         sync.RWMutex
	fingerprints map[string]uint64

	dbDelCh   chan string
	dbDelOnce sync.Once
	dbDelete  func(id string) error
}

func (h *ResourceEventHandler) dbDeleteFn() func(string) error {
	if h.dbDelete != nil {
		return h.dbDelete
	}
	if h.db != nil {
		return h.db.DeleteSearchableResource
	}
	return nil
}

func (h *ResourceEventHandler) deleteFromDBAsync(id string) {
	del := h.dbDeleteFn()
	if del == nil {
		return
	}
	h.dbDelOnce.Do(func() {
		h.dbDelCh = make(chan string, 1024)
		go func() {
			for qid := range h.dbDelCh {
				if err := del(qid); err != nil {
					log.Printf("Failed to delete resource %s from database: %v", qid, err)
				}
			}
		}()
	})
	select {
	case h.dbDelCh <- id:
	default:
		if err := del(id); err != nil {
			log.Printf("Failed to delete resource %s from database: %v", id, err)
		}
	}
}

func (h *ResourceEventHandler) forgetCluster(cluster string) {
	h.fpMu.Lock()
	docPrefix, kindPrefix := cluster+"/", "kind:"+cluster+":"
	for id := range h.fingerprints {
		if strings.HasPrefix(id, docPrefix) || strings.HasPrefix(id, kindPrefix) {
			delete(h.fingerprints, id)
		}
	}
	h.fpMu.Unlock()
}

// fingerprintSearchable returns a stable hash of only the fields used by search.
// Two events for the same resource with identical searchable contents produce the
// same hash and can be coalesced as no-ops.
func fingerprintSearchable(s storage.SearchableResource) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.ID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.Cluster))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.APIVersion))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.Name))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.Namespace))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(s.Category))
	_, _ = h.Write([]byte{0})
	if len(s.Labels) > 0 {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = h.Write([]byte(k))
			_, _ = h.Write([]byte{'='})
			_, _ = h.Write([]byte(s.Labels[k]))
			_, _ = h.Write([]byte{0})
		}
	}
	if len(s.Annotations) > 0 {
		keys := make([]string, 0, len(s.Annotations))
		for k := range s.Annotations {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = h.Write([]byte(k))
			_, _ = h.Write([]byte{'='})
			_, _ = h.Write([]byte(s.Annotations[k]))
			_, _ = h.Write([]byte{0})
		}
	}
	return h.Sum64()
}

func (h *ResourceEventHandler) OnAdd(cluster string, resource map[string]interface{}) (bool, error) {
	return h.OnAddWithCoords(cluster, resourceCoords{}, resource)
}

// OnAddWithCoords indexes an added or modified object. coords come from the
// items topic and pin the document ID to the same plural resource name the
// LIST sweep uses, so the two paths never produce two documents for one
// object.
func (h *ResourceEventHandler) OnAddWithCoords(cluster string, coords resourceCoords, resource map[string]interface{}) (bool, error) {
	if kind, ok := resource["kind"].(string); ok && utils.PluralizeKind(kind) == "events" {
		return false, nil
	}
	if coords.resource == "events" {
		return false, nil
	}
	searchable := h.convertToSearchable(cluster, coords, resource)
	fp := fingerprintSearchable(searchable)
	h.fpMu.RLock()
	prev, hasPrev := h.fingerprints[searchable.ID]
	h.fpMu.RUnlock()
	if hasPrev && prev == fp && h.index.HasDocument(searchable.ID) {
		return false, nil
	}
	if err := h.index.Index(searchable); err != nil {
		return false, err
	}
	h.fpMu.Lock()
	if h.fingerprints == nil {
		h.fingerprints = make(map[string]uint64, 1024)
	}
	h.fingerprints[searchable.ID] = fp
	h.fpMu.Unlock()
	if h.db != nil {
		h.persistResource(searchable)
	}
	return true, nil
}

func (h *ResourceEventHandler) OnUpdate(cluster string, oldResource, newResource map[string]interface{}) (bool, error) {
	return h.OnAdd(cluster, newResource)
}

func (h *ResourceEventHandler) filterChanged(resources []storage.SearchableResource) []storage.SearchableResource {
	changed := make([]storage.SearchableResource, 0, len(resources))
	h.fpMu.Lock()
	if h.fingerprints == nil {
		h.fingerprints = make(map[string]uint64, 1024)
	}
	for _, r := range resources {
		fp := fingerprintSearchable(r)
		if prev, ok := h.fingerprints[r.ID]; ok && prev == fp && h.index.HasDocument(r.ID) {
			continue
		}
		h.fingerprints[r.ID] = fp
		changed = append(changed, r)
	}
	h.fpMu.Unlock()
	return changed
}

func (h *ResourceEventHandler) OnDelete(cluster string, resource map[string]interface{}) error {
	return h.onDeleteWithCoords(cluster, resourceCoords{}, resource)
}

// forgetFingerprints drops the cached fingerprints for the given resource IDs so
// that if a resource with the same ID reappears it is treated as new and
// re-indexed. Used after reconciliation removes stale entries.
func (h *ResourceEventHandler) forgetFingerprints(ids []string) {
	if len(ids) == 0 {
		return
	}
	h.fpMu.Lock()
	for _, id := range ids {
		delete(h.fingerprints, id)
	}
	h.fpMu.Unlock()
}

// onDeleteWithCoords removes a resource from the index. Kubernetes watch DELETE
// events frequently strip kind/apiVersion from the object (tombstones,
// DeletedFinalStateUnknown), which previously produced an ID that didn't match
// the one used at index time, so the document was never removed. The topic's
// group/version/resource fill in whatever the object lacks so the
// reconstructed ID is byte-identical to the indexed one.
func (h *ResourceEventHandler) onDeleteWithCoords(cluster string, coords resourceCoords, resource map[string]interface{}) error {
	name, _ := resource["name"].(string)
	namespace, _ := resource["namespace"].(string)
	kind, _ := resource["kind"].(string)
	group, version := coords.group, coords.version
	if apiVersion, _ := resource["apiVersion"].(string); apiVersion != "" && (group == "" && version == "") {
		group, version = splitAPIVersion(apiVersion)
	}
	resourceName := coords.resource
	if resourceName == "" && kind != "" {
		resourceName = utils.PluralizeKind(kind)
	}
	if name == "" || resourceName == "" {
		return nil
	}
	id := storage.BuildResourceID(cluster, group, version, resourceName, namespace, name)
	if err := h.index.Remove(id); err != nil {
		// The reconstructed ID didn't match what was indexed — legacy docs from
		// before kind normalization, or deletes whose payload disagrees with the
		// indexed coordinates. Fall back to kind-agnostic removal.
		h.index.RemoveByCoordinates(cluster, namespace, name)
	}
	h.fpMu.Lock()
	delete(h.fingerprints, id)
	h.fpMu.Unlock()
	h.deleteFromDBAsync(id)
	return nil
}

func splitAPIVersion(apiVersion string) (group, version string) {
	if i := strings.IndexByte(apiVersion, '/'); i >= 0 {
		return apiVersion[:i], apiVersion[i+1:]
	}
	return "", apiVersion
}

func (h *ResourceEventHandler) convertToSearchable(cluster string, coords resourceCoords, resource map[string]interface{}) storage.SearchableResource {
	searchable := storage.SearchableResource{
		Cluster: cluster,
	}
	if name, ok := resource["name"].(string); ok {
		searchable.Name = name
	}
	if ns, ok := resource["namespace"].(string); ok {
		searchable.Namespace = ns
	}
	if kind, ok := resource["kind"].(string); ok && kind != "" {
		searchable.Kind = kind
	} else {
		searchable.Kind = coords.kind
	}
	searchable.Category = getCategoryForKind(searchable.Kind)
	if apiVersion, ok := resource["apiVersion"].(string); ok && apiVersion != "" {
		searchable.APIVersion = apiVersion
		searchable.Group, searchable.Version = splitAPIVersion(apiVersion)
	} else if coords.group != "" || coords.version != "" {
		searchable.Group, searchable.Version = coords.group, coords.version
		if coords.group != "" {
			searchable.APIVersion = coords.group + "/" + coords.version
		} else {
			searchable.APIVersion = coords.version
		}
	}
	// The topic's plural resource name wins over pluralizing the kind: it is
	// what discovery reported and what the LIST sweep used for its IDs.
	resourceName := coords.resource
	if resourceName == "" {
		resourceName = utils.PluralizeKind(searchable.Kind)
	}
	if labels, ok := resource["labels"].(map[string]interface{}); ok {
		searchable.Labels = make(map[string]string)
		for k, v := range labels {
			if s, ok := v.(string); ok {
				searchable.Labels[k] = s
			}
		}
	}
	if annotations, ok := resource["annotations"].(map[string]interface{}); ok {
		searchable.Annotations = make(map[string]string)
		for k, v := range annotations {
			if s, ok := v.(string); ok {
				searchable.Annotations[k] = s
			}
		}
	}
	searchable.ID = storage.BuildResourceID(cluster, searchable.Group, searchable.Version, resourceName, searchable.Namespace, searchable.Name)
	searchable.UpdatedAt = time.Now()
	return searchable
}

func (h *ResourceEventHandler) persistResource(searchable storage.SearchableResource) {
	if h.batchWriter == nil {
		return
	}
	dbResource := db.SearchableResource{
		ResourceID:      searchable.ID,
		Cluster:         searchable.Cluster,
		Kind:            searchable.Kind,
		APIVersion:      searchable.APIVersion,
		Name:            searchable.Name,
		Namespace:       searchable.Namespace,
		Description:     searchable.Description,
		Keywords:        strings.Join(searchable.Keywords, " "),
		Category:        searchable.Category,
		ResourceGroup:   searchable.Group,
		ResourceVersion: searchable.Version,
		CreatedAt:       searchable.CreatedAt,
		UpdatedAt:       searchable.UpdatedAt,
		IndexedAt:       time.Now(),
	}
	h.batchWriter.Add(dbResource)
}
