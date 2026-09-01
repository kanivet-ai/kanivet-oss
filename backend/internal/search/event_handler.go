package search

import (
	"fmt"
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
	if kind, ok := resource["kind"].(string); ok && utils.PluralizeKind(kind) == "events" {
		return false, nil
	}
	searchable := h.convertToSearchable(cluster, resource)
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
	return h.onDeleteWithCoords(cluster, "", "", "", resource)
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
// the one used at index time, so the document was never removed. The authoritative
// group/version/kind from the topic are used to fill any missing fields so the
// reconstructed ID is byte-identical to the indexed one.
func (h *ResourceEventHandler) onDeleteWithCoords(cluster, group, version, kind string, resource map[string]interface{}) error {
	name, _ := resource["name"].(string)
	namespace, _ := resource["namespace"].(string)
	if rk, _ := resource["kind"].(string); rk != "" {
		kind = rk
	}
	if apiVersion, _ := resource["apiVersion"].(string); apiVersion != "" {
		parts := strings.Split(apiVersion, "/")
		if len(parts) == 2 {
			group, version = parts[0], parts[1]
		} else {
			group, version = "", apiVersion
		}
	}
	if name == "" || kind == "" {
		return nil
	}
	var id string
	if namespace != "" {
		id = fmt.Sprintf("%s/%s/%s/%s/%s/%s", cluster, group, version, utils.PluralizeKind(kind), namespace, name)
	} else {
		id = fmt.Sprintf("%s/%s/%s/%s/%s", cluster, group, version, utils.PluralizeKind(kind), name)
	}
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

func (h *ResourceEventHandler) convertToSearchable(cluster string, resource map[string]interface{}) storage.SearchableResource {
	searchable := storage.SearchableResource{
		Cluster: cluster,
	}
	if name, ok := resource["name"].(string); ok {
		searchable.Name = name
	}
	if ns, ok := resource["namespace"].(string); ok {
		searchable.Namespace = ns
	}
	if kind, ok := resource["kind"].(string); ok {
		searchable.Kind = kind
		searchable.Category = getCategoryForKind(strings.ToLower(kind))
	}
	if apiVersion, ok := resource["apiVersion"].(string); ok {
		searchable.APIVersion = apiVersion
		parts := strings.Split(apiVersion, "/")
		if len(parts) == 2 {
			searchable.Group = parts[0]
			searchable.Version = parts[1]
		} else {
			searchable.Group = ""
			searchable.Version = apiVersion
		}
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
	if searchable.Namespace != "" {
		searchable.ID = fmt.Sprintf("%s/%s/%s/%s/%s/%s", cluster, searchable.Group, searchable.Version, utils.PluralizeKind(searchable.Kind), searchable.Namespace, searchable.Name)
	} else {
		searchable.ID = fmt.Sprintf("%s/%s/%s/%s/%s", cluster, searchable.Group, searchable.Version, utils.PluralizeKind(searchable.Kind), searchable.Name)
	}
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
