package storage

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"sort"
	"sync"
)

// DefaultSearchShards is the default number of search index shards. Sized to a
// small power of two so per-cluster writes spread across shards while keeping
// search fan-out cheap.
const DefaultSearchShards = 8

// ShardedIndex routes documents to one of N MemoryIndex shards keyed by cluster.
// All documents for a given cluster live on a single shard, so each shard's
// write lock only serializes writes for the clusters it owns — eliminating the
// cross-cluster lock contention of a single global index. Searches fan out
// across shards and merge, preserving the single-index result ordering.
type ShardedIndex struct {
	shards []*MemoryIndex
}

// NewShardedIndex creates a sharded index with n shards (n>=1).
func NewShardedIndex(n int) *ShardedIndex {
	if n < 1 {
		n = 1
	}
	shards := make([]*MemoryIndex, n)
	for i := range shards {
		shards[i] = NewMemoryIndex()
	}
	return &ShardedIndex{shards: shards}
}

func (s *ShardedIndex) shardForCluster(cluster string) *MemoryIndex {
	if len(s.shards) == 1 {
		return s.shards[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(cluster))
	return s.shards[h.Sum32()%uint32(len(s.shards))]
}

// SetEvictionCallback propagates an eviction callback to every shard.
func (s *ShardedIndex) SetEvictionCallback(cb EvictionCallback) {
	for _, sh := range s.shards {
		sh.SetEvictionCallback(cb)
	}
}

// SetWarmTier propagates a warm tier to every shard. Shards share the warm tier;
// it is responsible for its own concurrency.
func (s *ShardedIndex) SetWarmTier(wt *WarmTier) {
	for _, sh := range s.shards {
		sh.SetWarmTier(wt)
	}
}

// SetEvictionDisabled toggles eviction on every shard.
func (s *ShardedIndex) SetEvictionDisabled(disabled bool) {
	for _, sh := range s.shards {
		sh.SetEvictionDisabled(disabled)
	}
}

// SetupWarmTiers gives every shard its own on-disk warm tier so per-shard doc
// IDs can't collide across shards.
func (s *ShardedIndex) SetupWarmTiers(dir string) error {
	for i, sh := range s.shards {
		wt, err := NewWarmTier(filepath.Join(dir, fmt.Sprintf("shard%d", i)), nil)
		if err != nil {
			return err
		}
		sh.SetWarmTier(wt)
	}
	return nil
}

func (s *ShardedIndex) MaybeCompactAll() {
	for _, sh := range s.shards {
		sh.MaybeCompact()
	}
}

func (s *ShardedIndex) AtCapacity() bool {
	return s.atCapacity(MaxIndexDocuments)
}

func (s *ShardedIndex) atCapacity(threshold int) bool {
	for _, sh := range s.shards {
		if sh.DocumentCount() >= threshold {
			return true
		}
	}
	return false
}

func (s *ShardedIndex) RemoveCluster(cluster string) int {
	return s.shardForCluster(cluster).RemoveCluster(cluster)
}

// Index routes a document to its cluster's shard.
func (s *ShardedIndex) Index(resource SearchableResource) error {
	return s.shardForCluster(resource.Cluster).Index(resource)
}

// BatchIndexPrepared groups prepared resources by shard and indexes each group
// with a single lock acquisition per shard.
func (s *ShardedIndex) BatchIndexPrepared(prepared []PreparedResource) {
	if len(s.shards) == 1 {
		s.shards[0].BatchIndexPrepared(prepared)
		return
	}
	groups := make(map[int][]PreparedResource)
	for _, p := range prepared {
		idx := int(s.shardIndex(p.Resource.Cluster))
		groups[idx] = append(groups[idx], p)
	}
	for idx, group := range groups {
		s.shards[idx].BatchIndexPrepared(group)
	}
}

func (s *ShardedIndex) shardIndex(cluster string) uint32 {
	if len(s.shards) == 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(cluster))
	return h.Sum32() % uint32(len(s.shards))
}

// Remove deletes a document. The cluster is the first path segment of the ID
// (cluster/group/version/kind[/namespace]/name), so the shard can be derived
// without scanning every shard.
func (s *ShardedIndex) Remove(id string) error {
	cluster := clusterFromID(id)
	return s.shardForCluster(cluster).Remove(id)
}

func clusterFromID(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			return id[:i]
		}
	}
	return id
}

// RemoveByCoordinates removes documents matching cluster+namespace+name from the
// cluster's shard, regardless of kind form. Returns the number removed.
func (s *ShardedIndex) RemoveByCoordinates(cluster, namespace, name string) int {
	return s.shardForCluster(cluster).RemoveByCoordinates(cluster, namespace, name)
}

// ReconcileType removes stale documents of a resource type on the cluster's
// shard whose namespace/name is absent from liveKeys. Returns the removed IDs.
func (s *ShardedIndex) ReconcileType(cluster, group, version, kind string, liveKeys map[string]struct{}) []string {
	return s.shardForCluster(cluster).ReconcileType(cluster, group, version, kind, liveKeys)
}

// DocumentCount sums document counts across all shards.
func (s *ShardedIndex) DocumentCount() int {
	total := 0
	for _, sh := range s.shards {
		total += sh.DocumentCount()
	}
	return total
}

// Search fans out across shards (or routes to the relevant shards when the query
// is cluster-filtered) and merges the results, reproducing the single-index
// ordering: KindDefinition results first (capped at 5, by score), then regular
// results by score, then pagination applied once globally.
func (s *ShardedIndex) Search(query SearchQuery) ([]SearchResult, error) {
	targets := s.searchTargets(query)
	if len(targets) == 1 {
		return targets[0].Search(query)
	}

	// Strip pagination for per-shard queries so merging sees every candidate,
	// then paginate the merged set once.
	shardQuery := query
	shardQuery.Offset = 0
	shardQuery.Limit = 0

	results := make([][]SearchResult, len(targets))
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for i, sh := range targets {
		wg.Add(1)
		go func(i int, sh *MemoryIndex) {
			defer wg.Done()
			r, err := sh.Search(shardQuery)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			results[i] = r
		}(i, sh)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	var kindDefs, regular []SearchResult
	for _, shardResults := range results {
		for _, r := range shardResults {
			if r.Resource.Kind == "KindDefinition" {
				kindDefs = append(kindDefs, r)
			} else {
				regular = append(regular, r)
			}
		}
	}
	sort.Slice(kindDefs, func(i, j int) bool { return kindDefs[i].Score > kindDefs[j].Score })
	sort.Slice(regular, func(i, j int) bool { return regular[i].Score > regular[j].Score })
	if len(kindDefs) > 5 {
		kindDefs = kindDefs[:5]
	}
	merged := append(kindDefs, regular...)
	return paginate(merged, query.Offset, query.Limit), nil
}

// searchTargets returns the shards a query must hit. A cluster-filtered query
// only needs the shards owning those clusters; otherwise all shards.
func (s *ShardedIndex) searchTargets(query SearchQuery) []*MemoryIndex {
	clusters := query.Clusters
	if query.Cluster != "" {
		clusters = append([]string{query.Cluster}, clusters...)
	}
	if len(clusters) == 0 {
		return s.shards
	}
	seen := make(map[uint32]struct{}, len(clusters))
	var targets []*MemoryIndex
	for _, c := range clusters {
		idx := s.shardIndex(c)
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		targets = append(targets, s.shards[idx])
	}
	return targets
}

// FindKindsLike fans out across the relevant shards and concatenates results,
// honoring the limit across the merged set.
func (s *ShardedIndex) FindKindsLike(text string, clusters []string, limit int) []KindInfo {
	var targets []*MemoryIndex
	if len(clusters) == 0 {
		targets = s.shards
	} else {
		seen := make(map[uint32]struct{}, len(clusters))
		for _, c := range clusters {
			idx := s.shardIndex(c)
			if _, ok := seen[idx]; ok {
				continue
			}
			seen[idx] = struct{}{}
			targets = append(targets, s.shards[idx])
		}
	}
	var out []KindInfo
	for _, sh := range targets {
		out = append(out, sh.FindKindsLike(text, clusters, limit)...)
		if limit > 0 && len(out) >= limit {
			return out[:limit]
		}
	}
	return out
}

// SwapData distributes the documents of a freshly bulk-loaded IndexData into
// per-cluster shards. Each shard is rebuilt from the subset of documents
// belonging to the clusters it owns, then finalized for fuzzy search.
func (s *ShardedIndex) SwapData(newData *IndexData) {
	if len(s.shards) == 1 {
		s.shards[0].SwapData(newData)
		return
	}
	perShard := make([][]PreparedResource, len(s.shards))
	for _, compact := range newData.compactDocs {
		r := compact.ToSearchable(newData.pools)
		idx := s.shardIndex(r.Cluster)
		perShard[idx] = append(perShard[idx], prepareResource(r))
	}
	for i, prepared := range perShard {
		d := NewIndexData()
		BatchIndexToDataFast(d, prepared)
		FinalizeBulkLoad(d)
		s.shards[i].SwapData(d)
	}
}

// GetDocumentWithWarmTier looks up a document by ID on its cluster's shard.
func (s *ShardedIndex) GetDocumentWithWarmTier(id string) (SearchableResource, bool) {
	return s.shardForCluster(clusterFromID(id)).GetDocumentWithWarmTier(id)
}

// HasDocument reports whether the document's shard holds it.
func (s *ShardedIndex) HasDocument(id string) bool {
	return s.shardForCluster(clusterFromID(id)).HasDocument(id)
}

// Stats aggregates stats across shards.
func (s *ShardedIndex) Stats() IndexStats {
	var agg IndexStats
	clusterSet := make(map[string]struct{})
	for _, sh := range s.shards {
		st := sh.Stats()
		agg.TotalDocuments += st.TotalDocuments
		agg.IndexSize += st.IndexSize
		if st.LastUpdated.After(agg.LastUpdated) {
			agg.LastUpdated = st.LastUpdated
		}
		for _, c := range st.Clusters {
			clusterSet[c] = struct{}{}
		}
	}
	agg.Clusters = make([]string, 0, len(clusterSet))
	for c := range clusterSet {
		agg.Clusters = append(agg.Clusters, c)
	}
	return agg
}
