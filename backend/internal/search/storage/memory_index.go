package storage

import (
	"container/heap"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/kanivet/backend/internal/utils"
)

var (
	tokenizeRegex = regexp.MustCompile(`[^a-z0-9]+`)
	abbreviations = map[string]bool{
		"k8s": true, "api": true, "svc": true, "pv": true, "pvc": true,
		"ns": true, "sa": true, "cm": true, "ds": true, "rs": true,
		"hpa": true, "crd": true, "csr": true, "pdb": true, "rb": true,
		"crb": true, "sc": true, "sts": true, "ing": true, "ep": true,
		"db": true, "lb": true, "ui": true, "id": true, "ip": true,
		"cpu": true, "ram": true, "gpu": true, "ssd": true, "aws": true,
		"gcp": true, "tcp": true, "udp": true, "http": true, "https": true,
		"dns": true, "ssl": true, "tls": true, "jwt": true, "url": true,
	}
	levBufferPool = sync.Pool{New: func() interface{} { return make([]int, 512) }}
	tokensPool    = sync.Pool{New: func() interface{} { return make(map[string]float32, 64) }}
)

// buildTokens computes the weighted search tokens for a resource using a pooled
// map. The returned map MUST be handed back via putTokens once the caller is
// done reading from it. Weights mirror prepareResource exactly.
func buildTokens(r SearchableResource) map[string]float32 {
	tokens := tokensPool.Get().(map[string]float32)
	for _, t := range tokenize(r.Name) {
		tokens[t] += 3.0
	}
	for _, t := range prefixTokens(r.Name) {
		tokens[t] += 3.0
	}
	for _, t := range tokenize(r.Namespace) {
		tokens[t] += 2.0
	}
	for _, t := range tokenize(r.Kind) {
		tokens[t] += 3.4
	}
	for _, t := range prefixTokens(r.Kind) {
		tokens[t] += 3.4
	}
	if k := strings.ToLower(r.Kind); k != "" {
		for _, t := range tokenize(utils.PluralizeKind(k)) {
			tokens[t] += 2.6
		}
	}
	for _, t := range tokenize(r.Group) {
		tokens[t] += 0.8
	}
	for _, t := range tokenize(r.Version) {
		tokens[t] += 0.8
	}
	for _, t := range tokenize(r.Description) {
		tokens[t] += 1.2
	}
	for key, value := range r.Labels {
		for _, t := range tokenize(key) {
			tokens[t] += 1.2
		}
		for _, t := range tokenize(value) {
			tokens[t] += 1.2
		}
	}
	for key, value := range r.Annotations {
		for _, t := range tokenize(key) {
			tokens[t] += 1.0
		}
		if !indexableAnnotationValue(key, value) {
			continue
		}
		for _, t := range tokenize(value) {
			tokens[t] += 1.0
		}
	}
	for _, keyword := range r.Keywords {
		for _, t := range tokenize(keyword) {
			tokens[t] += 1.5
		}
	}
	return tokens
}

const maxAnnotationValueLen = 256

var (
	skipAnnotationKeys = map[string]bool{
		"kubectl.kubernetes.io/last-applied-configuration": true,
	}
	skipAnnotationKeyPrefixes = []string{
		"checksum/", "banzaicloud.io/last-applied", "kapp.k14s.io/original",
	}
)

func indexableAnnotationValue(key, value string) bool {
	if len(value) > maxAnnotationValueLen || skipAnnotationKeys[key] {
		return false
	}
	for _, p := range skipAnnotationKeyPrefixes {
		if strings.HasPrefix(key, p) {
			return false
		}
	}
	return true
}

func shouldPoolTokens(n int) bool { return n <= 4096 }

func shouldPoolLevBuffer(n int) bool { return n <= 4096 }

// putTokens clears and returns a tokens map to the pool.
func putTokens(tokens map[string]float32) {
	if !shouldPoolTokens(len(tokens)) {
		return
	}
	for k := range tokens {
		delete(tokens, k)
	}
	tokensPool.Put(tokens)
}

// SearchableResource represents a Kubernetes resource that can be searched
type SearchableResource struct {
	ID          string            `json:"id"`
	Cluster     string            `json:"cluster"`
	Kind        string            `json:"kind"`
	APIVersion  string            `json:"apiVersion"`
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Group       string            `json:"group"`
	Version     string            `json:"version"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Keywords    []string          `json:"keywords"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// SearchResult represents a search result with scoring and matches
type SearchResult struct {
	Resource SearchableResource `json:"resource"`
	Score    float64            `json:"score"`
	Matches  []SearchMatch      `json:"matches"`
}

// SearchMatch represents where a search term was found
type SearchMatch struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// SearchQuery represents a structured search query
type SearchQuery struct {
	Text       string            `json:"text"`
	Cluster    string            `json:"cluster"`
	Clusters   []string          `json:"clusters"`
	Namespaces []string          `json:"namespaces"`
	Kinds      []string          `json:"kinds"`
	Labels     map[string]string `json:"labels"`
	Filters    map[string]string `json:"filters"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
}

// KindInfo describes a resource kind present in the index
type KindInfo struct {
	Cluster    string `json:"cluster"`
	Kind       string `json:"kind"`
	Group      string `json:"group"`
	Version    string `json:"version"`
	Namespaced bool   `json:"namespaced"`
}

// IndexStats provides statistics about the search index
type IndexStats struct {
	TotalDocuments int       `json:"totalDocuments"`
	IndexSize      int64     `json:"indexSize"`
	LastUpdated    time.Time `json:"lastUpdated"`
	Clusters       []string  `json:"clusters"`
}

// SearchCategories maps resource kinds to categories
var SearchCategories = map[string]string{
	"pod":                   "Workloads",
	"deployment":            "Workloads",
	"replicaset":            "Workloads",
	"statefulset":           "Workloads",
	"daemonset":             "Workloads",
	"job":                   "Workloads",
	"cronjob":               "Workloads",
	"service":               "Networking",
	"ingress":               "Networking",
	"networkpolicy":         "Networking",
	"configmap":             "Configuration",
	"secret":                "Configuration",
	"persistentvolume":      "Storage",
	"persistentvolumeclaim": "Storage",
	"storageclass":          "Storage",
	"namespace":             "Cluster",
	"node":                  "Cluster",
	"serviceaccount":        "Security",
	"role":                  "Security",
	"rolebinding":           "Security",
	"clusterrole":           "Security",
	"clusterrolebinding":    "Security",
}

// MaxIndexDocuments is the maximum number of documents to keep in memory
// When exceeded, least recently used documents are evicted (but remain in DB)
const MaxIndexDocuments = 100000

const (
	maxTermLen              = 64
	compactRemovedThreshold = 20000
	compactTermRatio        = 4
	compactTermSlack        = 10000
)

// LRUEntry tracks access time for eviction
type LRUEntry struct {
	AccessTime int64
	DocID      uint32
}

func (d *IndexData) internTerm(term string) uint32 {
	idx, ok := d.invertedIdx.GetTermIndex(term)
	if ok {
		return idx
	}
	d.invertedIdx.getOrCreateTerm(term)
	return d.invertedIdx.termIdx[term]
}

func (d *IndexData) getTerm(idx uint32) string {
	return d.invertedIdx.GetTerm(idx)
}

func (d *IndexData) getDocument(id uint32) (SearchableResource, bool) {
	compact, ok := d.compactDocs[id]
	if !ok {
		return SearchableResource{}, false
	}
	return compact.ToSearchable(d.pools), true
}

func (d *IndexData) getDocumentByStringID(id string) (SearchableResource, bool) {
	docID, ok := d.pools.IDs.Lookup(id)
	if !ok {
		return SearchableResource{}, false
	}
	return d.getDocument(docID)
}

func (d *IndexData) documentCount() int {
	return len(d.compactDocs)
}

// IndexData holds all mutable index data for atomic swap (exported for bulk loading)
type IndexData struct {
	compactDocs map[uint32]CompactResource
	pools       *InternPools
	invertedIdx *CompactInvertedIndex
	docTerms    map[uint32][]uint32
	clusterDocs map[uint32]int
	bktName     *bkTree
	bktKind     *bkTree
	nameDocs    map[string]map[uint32]struct{}
	kindDocs    map[string]map[uint32]struct{}
	nameTerms   map[string]struct{}
	kindTerms   map[string]struct{}
	accessTimes map[uint32]int64
	accessGen   int64
}

func newIndexData() *IndexData {
	return newIndexDataWithCapacity(0)
}

func newIndexDataWithCapacity(cap int) *IndexData {
	if cap <= 0 {
		cap = 1000
	}
	return &IndexData{
		compactDocs: make(map[uint32]CompactResource, cap),
		pools:       NewInternPools(),
		invertedIdx: NewCompactInvertedIndex(),
		docTerms:    make(map[uint32][]uint32, cap),
		clusterDocs: make(map[uint32]int, 50),
		bktName:     &bkTree{},
		bktKind:     &bkTree{},
		nameDocs:    make(map[string]map[uint32]struct{}, cap),
		kindDocs:    make(map[string]map[uint32]struct{}, 500),
		nameTerms:   make(map[string]struct{}, cap),
		kindTerms:   make(map[string]struct{}, 500),
		accessTimes: make(map[uint32]int64, cap),
		accessGen:   0,
	}
}

func NewIndexDataWithCapacity(cap int) *IndexData {
	return newIndexDataWithCapacity(cap)
}

// EvictionCallback is called when documents are evicted from memory
// The callback should persist evicted documents to DB if needed
type EvictionCallback func(evicted []SearchableResource)

// MemoryIndex implements an in-memory search index with LRU eviction
type MemoryIndex struct {
	mu                  sync.RWMutex
	data                atomic.Pointer[IndexData]
	stats               IndexStats
	onEvict             EvictionCallback
	evictionDisabled    bool
	warmTier            *WarmTier
	removedSinceRebuild int
}

// NewMemoryIndex creates a new memory-based search index
func NewMemoryIndex() *MemoryIndex {
	idx := &MemoryIndex{
		stats: IndexStats{LastUpdated: time.Now()},
	}
	idx.data.Store(newIndexData())
	return idx
}

// SetEvictionCallback sets a callback to be called when documents are evicted
func (idx *MemoryIndex) SetEvictionCallback(cb EvictionCallback) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.onEvict = cb
}

// SetEvictionDisabled controls whether eviction is enabled (disabled during bulk loading)
func (idx *MemoryIndex) SetEvictionDisabled(disabled bool) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.evictionDisabled = disabled
}

// SetWarmTier sets the warm tier for storing evicted documents
func (idx *MemoryIndex) SetWarmTier(wt *WarmTier) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.warmTier = wt
}

// getData returns the current index data (for read operations)
func (idx *MemoryIndex) getData() *IndexData {
	return idx.data.Load()
}

// Index adds or updates a resource in the search index
// If the index exceeds MaxIndexDocuments, LRU documents are evicted
func (idx *MemoryIndex) Index(resource SearchableResource) error {
	tokens := buildTokens(resource)

	idx.mu.Lock()
	d := idx.getData()
	compact := CompactFromSearchable(resource, d.pools)
	docID := compact.ID

	if old, exists := d.compactDocs[docID]; exists {
		idx.removeFromInvertedIndexFastLocked(d, docID)
		idx.removeFieldMapsLocked(d, docID, old.ToSearchable(d.pools))
		d.clusterDocs[old.Cluster]--
		if d.clusterDocs[old.Cluster] <= 0 {
			delete(d.clusterDocs, old.Cluster)
		}
	}

	d.compactDocs[docID] = compact
	d.clusterDocs[compact.Cluster]++
	termIndices := make([]uint32, 0, len(tokens))
	for token, boost := range tokens {
		termIdx := d.invertedIdx.AddAndGetIndex(token, docID, boost)
		termIndices = append(termIndices, termIdx)
	}
	d.docTerms[docID] = termIndices
	idx.addFieldMapsLocked(d, docID, resource)

	d.accessGen++
	d.accessTimes[docID] = d.accessGen

	var evicted []SearchableResource
	if !idx.evictionDisabled {
		// Batch eviction: only trigger when we're at least 1% over the limit, then
		// evict to ~99% of the limit in one shot. This amortizes the cost of the
		// O(N) selectOldest scan across many add operations.
		const evictSlack = MaxIndexDocuments / 100 // 1% slack
		if len(d.compactDocs) > MaxIndexDocuments+evictSlack {
			target := MaxIndexDocuments - evictSlack
			if target < 0 {
				target = 0
			}
			evicted = idx.evictLRULocked(d, len(d.compactDocs)-target)
		}
	}

	idx.updateStatsIncrementalLocked(d)
	onEvict := idx.onEvict
	idx.mu.Unlock()
	putTokens(tokens)

	if len(evicted) > 0 && onEvict != nil {
		onEvict(evicted)
	}

	return nil
}

// removeDocLocked removes one document and every index structure entry that
// points at it. Caller must hold idx.mu.
func (idx *MemoryIndex) removeDocLocked(d *IndexData, id uint32) bool {
	compact, ok := d.compactDocs[id]
	if !ok {
		return false
	}
	res := compact.ToSearchable(d.pools)
	idx.removeFromInvertedIndexFastLocked(d, id)
	idx.removeFieldMapsLocked(d, id, res)
	d.clusterDocs[compact.Cluster]--
	if d.clusterDocs[compact.Cluster] <= 0 {
		delete(d.clusterDocs, compact.Cluster)
	}
	delete(d.compactDocs, id)
	delete(d.accessTimes, id)
	idx.removedSinceRebuild++
	return true
}

// Remove removes a resource from the index
func (idx *MemoryIndex) Remove(id string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	d := idx.getData()

	docID, ok := d.pools.IDs.Lookup(id)
	if !ok || !idx.removeDocLocked(d, docID) {
		return fmt.Errorf("document %s not found", id)
	}
	idx.updateStatsIncrementalLocked(d)
	return nil
}

// RemoveByCoordinates removes every document matching cluster+namespace+name
// regardless of the kind form stored in its ID. This is the fallback for delete
// events whose payload kind doesn't match what was indexed (e.g. a stripped
// watch DELETE where the kind became the plural resource name). It returns the
// number of documents removed.
func (idx *MemoryIndex) RemoveByCoordinates(cluster, namespace, name string) int {
	if name == "" {
		return 0
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	d := idx.getData()

	ids := d.nameDocs[strings.ToLower(name)]
	if len(ids) == 0 {
		return 0
	}
	var victims []uint32
	for id := range ids {
		compact, ok := d.compactDocs[id]
		if !ok {
			continue
		}
		if d.pools.Clusters.Get(compact.Cluster) != cluster {
			continue
		}
		if d.pools.Namespaces.Get(compact.Namespace) != namespace {
			continue
		}
		victims = append(victims, id)
	}
	for _, id := range victims {
		idx.removeDocLocked(d, id)
	}
	if len(victims) > 0 {
		idx.updateStatsIncrementalLocked(d)
	}
	return len(victims)
}

// ReconcileType removes indexed documents of a resource type (identified by
// cluster+group+version) whose namespace/name is no longer present in the live
// set obtained from the cluster. liveKeys holds "namespace/name" (or "name" for
// cluster-scoped resources). This is kind-form agnostic: it matches on the
// stable cluster/group/version + namespace/name identity, so it cleans up stale
// entries regardless of whether they were indexed with the singular Kind or the
// plural resource name. KindDefinition documents are never touched. Returns the
// string IDs of the removed documents.
func (idx *MemoryIndex) ReconcileType(cluster, group, version string, liveKeys map[string]struct{}) []string {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	d := idx.getData()

	var victims []uint32
	for id, compact := range d.compactDocs {
		if d.pools.Kinds.Get(compact.Kind) == "KindDefinition" {
			continue
		}
		if d.pools.Clusters.Get(compact.Cluster) != cluster ||
			d.pools.Groups.Get(compact.Group) != group ||
			d.pools.Versions.Get(compact.Version) != version {
			continue
		}
		ns := d.pools.Namespaces.Get(compact.Namespace)
		name := d.pools.Names.Get(compact.Name)
		key := name
		if ns != "" {
			key = ns + "/" + name
		}
		if _, alive := liveKeys[key]; !alive {
			victims = append(victims, id)
		}
	}
	removed := make([]string, 0, len(victims))
	for _, id := range victims {
		if compact, ok := d.compactDocs[id]; ok {
			removed = append(removed, d.pools.IDs.Get(compact.ID))
			idx.removeDocLocked(d, id)
		}
	}
	if len(removed) > 0 {
		idx.updateStatsIncrementalLocked(d)
	}
	return removed
}

// evictLRULocked evicts the N least recently used documents from the index.
// Must be called with idx.mu held. Returns evicted documents for persistence.
//
// Selecting the K oldest entries is done with a partial selection rather than a
// full sort: an O(N) min scan when K==1 (the steady-state Index() path that
// goes one over the cap), and an O(N log K) bounded max-heap for K>=2 (bulk
// loads that may exceed the cap by many entries). KindDefinition documents are
// excluded from eviction.
func (idx *MemoryIndex) evictLRULocked(d *IndexData, count int) []SearchableResource {
	if count <= 0 || len(d.compactDocs) == 0 {
		return nil
	}

	victims := idx.selectOldestLocked(d, count)
	if len(victims) == 0 {
		return nil
	}

	evicted := make([]SearchableResource, 0, len(victims))
	for _, id := range victims {
		compact, exists := d.compactDocs[id]
		if !exists {
			continue
		}
		if idx.warmTier != nil {
			idx.warmTier.Store(compact)
		}
		evicted = append(evicted, compact.ToSearchable(d.pools))
		idx.removeDocLocked(d, id)
	}

	return evicted
}

// selectOldestLocked returns up to count DocIDs with the smallest AccessTime,
// skipping documents whose Kind resolves to "KindDefinition". The returned
// slice is unordered; callers only need set membership for eviction.
// Caller must hold idx.mu.
func (idx *MemoryIndex) selectOldestLocked(d *IndexData, count int) []uint32 {
	isProtected := func(id uint32) bool {
		compact, ok := d.compactDocs[id]
		return ok && d.pools.Kinds.Get(compact.Kind) == "KindDefinition"
	}

	if count == 1 {
		var (
			oldestID   uint32
			oldestTime int64
			haveOldest bool
		)
		for id, accessTime := range d.accessTimes {
			if isProtected(id) {
				continue
			}
			if !haveOldest || accessTime < oldestTime {
				oldestTime = accessTime
				oldestID = id
				haveOldest = true
			}
		}
		if !haveOldest {
			return nil
		}
		return []uint32{oldestID}
	}

	// K>=2: keep the K smallest using a bounded max-heap of size K.
	// Heap top is the largest of the K-best so far; smaller candidates displace it.
	h := make(lruMaxHeap, 0, count)
	for id, accessTime := range d.accessTimes {
		if isProtected(id) {
			continue
		}
		entry := LRUEntry{AccessTime: accessTime, DocID: id}
		if len(h) < count {
			heap.Push(&h, entry)
			continue
		}
		if entry.AccessTime < h[0].AccessTime {
			h[0] = entry
			heap.Fix(&h, 0)
		}
	}
	if len(h) == 0 {
		return nil
	}
	ids := make([]uint32, len(h))
	for i, e := range h {
		ids[i] = e.DocID
	}
	return ids
}

// lruMaxHeap is a max-heap by AccessTime — the entry with the largest
// AccessTime sits at index 0, so it's the first to be displaced when a smaller
// (older) entry arrives during selection.
type lruMaxHeap []LRUEntry

func (h lruMaxHeap) Len() int            { return len(h) }
func (h lruMaxHeap) Less(i, j int) bool  { return h[i].AccessTime > h[j].AccessTime }
func (h lruMaxHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lruMaxHeap) Push(x interface{}) { *h = append(*h, x.(LRUEntry)) }
func (h *lruMaxHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// GetAllDocuments returns all documents in the index
func (idx *MemoryIndex) GetAllDocuments() []SearchableResource {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d := idx.getData()
	docs := make([]SearchableResource, 0, len(d.compactDocs))
	for _, compact := range d.compactDocs {
		docs = append(docs, compact.ToSearchable(d.pools))
	}
	return docs
}

// RLock acquires a read lock (kept for API compatibility but no longer needed for searches)
func (idx *MemoryIndex) RLock() {
	idx.mu.RLock()
}

// RUnlock releases a read lock
func (idx *MemoryIndex) RUnlock() {
	idx.mu.RUnlock()
}

// Search performs a search query with improved fuzzy matching
func (idx *MemoryIndex) Search(query SearchQuery) ([]SearchResult, error) {
	if query.Text == "" && len(query.Filters) == 0 {
		return []SearchResult{}, nil
	}

	idx.mu.RLock()
	d := idx.getData()

	type candidateDoc struct {
		id       uint32
		resource SearchableResource
		posting  float32
	}
	var candidateDocs []candidateDoc

	if query.Text != "" {
		queryLower := strings.ToLower(query.Text)
		qForms := uniqueStrings([]string{queryLower, utils.PluralizeKind(queryLower)})
		seen := make(map[uint32]bool)

		addCandidate := func(id uint32, postingScore float32) {
			if seen[id] {
				return
			}
			if compact, ok := d.compactDocs[id]; ok {
				if idx.matchesFiltersCompact(d, compact, query) {
					seen[id] = true
					candidateDocs = append(candidateDocs, candidateDoc{
						id:       id,
						resource: compact.ToSearchable(d.pools),
						posting:  postingScore,
					})
				}
			}
		}

		qTerms := uniqueStrings(append(append([]string{}, qForms...), tokenize(queryLower)...))
		for _, qf := range qTerms {
			if posting := d.invertedIdx.GetPostingList(qf); posting != nil {
				posting.Iterate(func(id uint32, score float32) { addCandidate(id, score) })
			}
		}
		if len(queryLower) > 2 {
			r := fuzzyRadius(queryLower)
			for _, term := range d.bktName.Nearby(queryLower, r) {
				if ids := d.nameDocs[term]; ids != nil {
					for id := range ids {
						addCandidate(id, 0)
					}
				}
			}
			for _, term := range d.bktKind.Nearby(queryLower, r) {
				if ids := d.kindDocs[term]; ids != nil {
					for id := range ids {
						addCandidate(id, 0)
					}
				}
			}
			if kindDefIDs := d.kindDocs["kinddefinition"]; kindDefIDs != nil {
				for id := range kindDefIDs {
					if compact, ok := d.compactDocs[id]; ok {
						name := d.pools.Names.Get(compact.Name)
						if strings.Contains(strings.ToLower(name), queryLower) {
							addCandidate(id, 0)
						}
					}
				}
			}
		}
		if len(candidateDocs) == 0 {
			for docID, compact := range d.compactDocs {
				if idx.matchesFiltersCompact(d, compact, query) {
					candidateDocs = append(candidateDocs, candidateDoc{
						id:       docID,
						resource: compact.ToSearchable(d.pools),
					})
				}
			}
		}
		idx.mu.RUnlock()

		scores := make(map[uint32]float64, len(candidateDocs))
		matches := make(map[uint32][]SearchMatch, len(candidateDocs))
		for _, cd := range candidateDocs {
			base := float64(cd.posting)
			var aggMatches []SearchMatch
			for _, qf := range qForms {
				s, m := idx.calculateExactScore(cd.resource, qf)
				base += s
				aggMatches = append(aggMatches, m...)
				s, m = idx.calculatePrefixScore(cd.resource, qf)
				base += s
				aggMatches = append(aggMatches, m...)
				s, m = idx.calculateSubstringScore(cd.resource, qf)
				base += s
				aggMatches = append(aggMatches, m...)
				if len(qf) > 2 {
					s, m = idx.calculateFuzzyScore(cd.resource, qf)
					base += s
					aggMatches = append(aggMatches, m...)
				}
			}
			if base > 0 {
				boost := idx.getResourceBoost(cd.resource)
				scores[cd.id] = base * boost
				matches[cd.id] = idx.deduplicateMatches(aggMatches)
			}
		}

		var kindDefResults, regularResults []SearchResult
		for _, cd := range candidateDocs {
			if score := scores[cd.id]; score > 0 {
				result := SearchResult{Resource: cd.resource, Score: score, Matches: matches[cd.id]}
				if cd.resource.Kind == "KindDefinition" {
					kindDefResults = append(kindDefResults, result)
				} else {
					regularResults = append(regularResults, result)
				}
			}
		}
		sort.Slice(kindDefResults, func(i, j int) bool { return kindDefResults[i].Score > kindDefResults[j].Score })
		sort.Slice(regularResults, func(i, j int) bool { return regularResults[i].Score > regularResults[j].Score })
		if len(kindDefResults) > 5 {
			kindDefResults = kindDefResults[:5]
		}
		return paginate(append(kindDefResults, regularResults...), query.Offset, query.Limit), nil
	}

	for docID, compact := range d.compactDocs {
		if idx.matchesFiltersCompact(d, compact, query) {
			candidateDocs = append(candidateDocs, candidateDoc{
				id:       docID,
				resource: compact.ToSearchable(d.pools),
			})
		}
	}
	idx.mu.RUnlock()

	results := make([]SearchResult, 0, len(candidateDocs))
	for _, cd := range candidateDocs {
		results = append(results, SearchResult{Resource: cd.resource, Score: 1.0})
	}
	return paginate(results, query.Offset, query.Limit), nil
}

func paginate(results []SearchResult, offset, limit int) []SearchResult {
	if offset > 0 {
		if offset >= len(results) {
			return []SearchResult{}
		}
		results = results[offset:]
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

// ListResourceIDsByType returns the string IDs of all indexed documents of a kind in a cluster
func (idx *MemoryIndex) ListResourceIDsByType(cluster, group, version, kind string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d := idx.getData()
	ids := make([]string, 0, 64)
	for id := range d.kindDocs[strings.ToLower(kind)] {
		compact, ok := d.compactDocs[id]
		if !ok {
			continue
		}
		if d.pools.Clusters.Get(compact.Cluster) != cluster ||
			d.pools.Groups.Get(compact.Group) != group ||
			d.pools.Versions.Get(compact.Version) != version {
			continue
		}
		ids = append(ids, d.pools.IDs.Get(compact.ID))
	}
	return ids
}

// FindKindsLike returns the distinct kinds in the index whose name matches text,
// restricted to the given clusters (all clusters when empty), prefix matches first
func (idx *MemoryIndex) FindKindsLike(text string, clusters []string, limit int) []KindInfo {
	q := strings.ToLower(strings.TrimSpace(text))
	if q == "" {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d := idx.getData()
	type comboKey struct{ cluster, kind, group, version uint32 }
	seen := make(map[comboKey]*KindInfo)
	var prefix, contains []*KindInfo
	terms := make([]string, 0, len(d.kindTerms))
	for term := range d.kindTerms {
		if term != "kinddefinition" && strings.Contains(term, q) {
			terms = append(terms, term)
		}
	}
	sort.Strings(terms)
	for _, term := range terms {
		for id := range d.kindDocs[term] {
			compact, ok := d.compactDocs[id]
			if !ok {
				continue
			}
			if len(clusters) > 0 && !containsString(clusters, d.pools.Clusters.Get(compact.Cluster)) {
				continue
			}
			key := comboKey{compact.Cluster, compact.Kind, compact.Group, compact.Version}
			info := seen[key]
			if info == nil {
				info = &KindInfo{
					Cluster: d.pools.Clusters.Get(compact.Cluster),
					Kind:    d.pools.Kinds.Get(compact.Kind),
					Group:   d.pools.Groups.Get(compact.Group),
					Version: d.pools.Versions.Get(compact.Version),
				}
				seen[key] = info
				if strings.HasPrefix(term, q) {
					prefix = append(prefix, info)
				} else {
					contains = append(contains, info)
				}
			}
			if !info.Namespaced && d.pools.Namespaces.Get(compact.Namespace) != "" {
				info.Namespaced = true
			}
		}
	}
	out := make([]KindInfo, 0, len(prefix)+len(contains))
	for _, list := range [][]*KindInfo{prefix, contains} {
		for _, info := range list {
			if limit > 0 && len(out) >= limit {
				return out
			}
			out = append(out, *info)
		}
	}
	return out
}

// Suggestions returns indexed kind and name terms starting with prefix
func (idx *MemoryIndex) Suggestions(prefix string, limit int) []string {
	q := strings.ToLower(strings.TrimSpace(prefix))
	if q == "" {
		return nil
	}
	idx.mu.RLock()
	d := idx.getData()
	matches := make([]string, 0, 32)
	for _, terms := range []map[string]struct{}{d.kindTerms, d.nameTerms} {
		for term := range terms {
			if strings.HasPrefix(term, q) {
				matches = append(matches, term)
			}
		}
	}
	idx.mu.RUnlock()
	sort.Strings(matches)
	out := make([]string, 0, len(matches))
	for i, m := range matches {
		if i > 0 && m == matches[i-1] {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

type bkNode struct {
	term     string
	children map[int]*bkNode
}
type bkTree struct{ root *bkNode }

func (t *bkTree) Insert(term string) {
	if term == "" {
		return
	}
	if t.root == nil {
		t.root = &bkNode{term: term, children: make(map[int]*bkNode, 4)}
		return
	}
	n := t.root
	for {
		d := levenshteinDistance(term, n.term)
		c := n.children[d]
		if c == nil {
			if n.children == nil {
				n.children = make(map[int]*bkNode, 4)
			}
			n.children[d] = &bkNode{term: term, children: make(map[int]*bkNode, 4)}
			return
		}
		n = c
	}
}

func (t *bkTree) Nearby(term string, maxD int) []string {
	if t.root == nil || term == "" {
		return nil
	}
	res := make([]string, 0, 16)
	var dfs func(*bkNode)
	dfs = func(n *bkNode) {
		d := levenshteinDistance(term, n.term)
		if d <= maxD {
			res = append(res, n.term)
		}
		lo := d - maxD
		hi := d + maxD
		for e, c := range n.children {
			if e >= lo && e <= hi {
				dfs(c)
			}
		}
	}
	dfs(t.root)
	return res
}

func (idx *MemoryIndex) addFieldMapsLocked(d *IndexData, id uint32, r SearchableResource) {
	if r.Name != "" {
		k := strings.ToLower(r.Name)
		m := d.nameDocs[k]
		if m == nil {
			m = make(map[uint32]struct{}, 8)
			d.nameDocs[k] = m
		}
		m[id] = struct{}{}
		if _, ok := d.nameTerms[k]; !ok {
			d.bktName.Insert(k)
			d.nameTerms[k] = struct{}{}
		}
	}
	if r.Kind != "" {
		k := strings.ToLower(r.Kind)
		m := d.kindDocs[k]
		if m == nil {
			m = make(map[uint32]struct{}, 8)
			d.kindDocs[k] = m
		}
		m[id] = struct{}{}
		if _, ok := d.kindTerms[k]; !ok {
			d.bktKind.Insert(k)
			d.kindTerms[k] = struct{}{}
		}
	}
}

func (idx *MemoryIndex) removeFieldMapsLocked(d *IndexData, id uint32, r SearchableResource) {
	if r.Name != "" {
		k := strings.ToLower(r.Name)
		if m := d.nameDocs[k]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(d.nameDocs, k)
			}
		}
	}
	if r.Kind != "" {
		k := strings.ToLower(r.Kind)
		if m := d.kindDocs[k]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(d.kindDocs, k)
			}
		}
	}
}

func fuzzyRadius(q string) int {
	l := len(q)
	if l <= 4 {
		return 1
	}
	if l <= 8 {
		return 2
	}
	return 3
}

type PreparedResource struct {
	Resource SearchableResource
	Tokens   map[string]float64
	TermList []string
}

func prepareResource(r SearchableResource) PreparedResource {
	pooled := buildTokens(r)
	tokens := make(map[string]float64, len(pooled))
	termList := make([]string, 0, len(pooled))
	for t, b := range pooled {
		tokens[t] = float64(b)
		termList = append(termList, t)
	}
	putTokens(pooled)
	return PreparedResource{Resource: r, Tokens: tokens, TermList: termList}
}

// BatchIndex indexes multiple resources with a single lock acquisition
func (idx *MemoryIndex) BatchIndex(resources []SearchableResource) error {
	if len(resources) == 0 {
		return nil
	}
	prepared := make([]PreparedResource, len(resources))
	for i, r := range resources {
		prepared[i] = prepareResource(r)
	}
	idx.BatchIndexPrepared(prepared)
	return nil
}

// BatchIndexPrepared indexes already-prepared resources (for parallel tokenization)
func (idx *MemoryIndex) BatchIndexPrepared(prepared []PreparedResource) {
	if len(prepared) == 0 {
		return
	}
	idx.mu.Lock()
	d := idx.getData()
	for _, p := range prepared {
		compact := CompactFromSearchable(p.Resource, d.pools)
		docID := compact.ID
		if old, exists := d.compactDocs[docID]; exists {
			idx.removeFromInvertedIndexFastLocked(d, docID)
			idx.removeFieldMapsLocked(d, docID, old.ToSearchable(d.pools))
			d.clusterDocs[old.Cluster]--
			if d.clusterDocs[old.Cluster] <= 0 {
				delete(d.clusterDocs, old.Cluster)
			}
		}
		d.compactDocs[docID] = compact
		d.clusterDocs[compact.Cluster]++
		termIndices := make([]uint32, 0, len(p.Tokens))
		for token, boost := range p.Tokens {
			termIdx := d.invertedIdx.AddAndGetIndex(token, docID, float32(boost))
			termIndices = append(termIndices, termIdx)
		}
		d.docTerms[docID] = termIndices
		idx.addFieldMapsLocked(d, docID, p.Resource)
		d.accessGen++
		d.accessTimes[docID] = d.accessGen
	}

	var evicted []SearchableResource
	if !idx.evictionDisabled {
		const evictSlack = MaxIndexDocuments / 100
		if len(d.compactDocs) > MaxIndexDocuments+evictSlack {
			target := MaxIndexDocuments - evictSlack
			if target < 0 {
				target = 0
			}
			evicted = idx.evictLRULocked(d, len(d.compactDocs)-target)
		}
	}

	idx.updateStatsLocked(d)
	onEvict := idx.onEvict
	idx.mu.Unlock()

	if len(evicted) > 0 && onEvict != nil {
		onEvict(evicted)
	}
}

// BatchIndexToData indexes prepared resources directly into an indexData struct (for bulk loading)
// Note: Does not perform eviction - caller should handle eviction after bulk loading
func BatchIndexToData(d *IndexData, prepared []PreparedResource) {
	for _, p := range prepared {
		compact := CompactFromSearchable(p.Resource, d.pools)
		docID := compact.ID
		if old, exists := d.compactDocs[docID]; exists {
			removeFromInvertedIndexFastData(d, docID)
			removeFieldMapsData(d, docID, old.ToSearchable(d.pools))
			d.clusterDocs[old.Cluster]--
			if d.clusterDocs[old.Cluster] <= 0 {
				delete(d.clusterDocs, old.Cluster)
			}
		}
		d.compactDocs[docID] = compact
		d.clusterDocs[compact.Cluster]++
		termIndices := make([]uint32, 0, len(p.Tokens))
		for token, boost := range p.Tokens {
			termIdx := d.invertedIdx.AddAndGetIndex(token, docID, float32(boost))
			termIndices = append(termIndices, termIdx)
		}
		d.docTerms[docID] = termIndices
		addFieldMapsData(d, docID, p.Resource)
		d.accessGen++
		d.accessTimes[docID] = d.accessGen
	}
}

// BatchIndexToDataFast is optimized for bulk loading - defers BK-tree building and posting list sorting
// Call FinalizeBulkLoad after all batches are indexed
func BatchIndexToDataFast(d *IndexData, prepared []PreparedResource) {
	for _, p := range prepared {
		compact := CompactFromSearchable(p.Resource, d.pools)
		docID := compact.ID
		if old, exists := d.compactDocs[docID]; exists {
			removeFromInvertedIndexFastData(d, docID)
			removeFieldMapsDataNoBKT(d, docID, old.ToSearchable(d.pools))
			d.clusterDocs[old.Cluster]--
			if d.clusterDocs[old.Cluster] <= 0 {
				delete(d.clusterDocs, old.Cluster)
			}
		}
		d.compactDocs[docID] = compact
		d.clusterDocs[compact.Cluster]++
		termIndices := make([]uint32, 0, len(p.Tokens))
		for token, boost := range p.Tokens {
			termIdx := d.invertedIdx.AddUnsorted(token, docID, float32(boost))
			termIndices = append(termIndices, termIdx)
		}
		d.docTerms[docID] = termIndices
		addFieldMapsDataNoBKT(d, docID, p.Resource)
		d.accessGen++
		d.accessTimes[docID] = d.accessGen
	}
}

// FinalizeBulkLoad builds BK-trees and sorts posting lists after bulk loading is complete
func FinalizeBulkLoad(d *IndexData) {
	d.invertedIdx.FinalizeAll()
	names := make([]string, 0, len(d.nameTerms))
	for term := range d.nameTerms {
		names = append(names, term)
	}
	sort.Strings(names)
	d.bktName = buildBKTreeBalanced(names)
	kinds := make([]string, 0, len(d.kindTerms))
	for term := range d.kindTerms {
		kinds = append(kinds, term)
	}
	sort.Strings(kinds)
	d.bktKind = buildBKTreeBalanced(kinds)
}

func buildBKTreeBalanced(terms []string) *bkTree {
	if len(terms) == 0 {
		return &bkTree{}
	}
	mid := len(terms) / 2
	tree := &bkTree{}
	tree.root = &bkNode{term: terms[mid], children: make(map[int]*bkNode, 4)}
	for i, t := range terms {
		if i != mid {
			tree.Insert(t)
		}
	}
	return tree
}

func addFieldMapsDataNoBKT(d *IndexData, id uint32, r SearchableResource) {
	if r.Name != "" {
		k := strings.ToLower(r.Name)
		m := d.nameDocs[k]
		if m == nil {
			m = make(map[uint32]struct{}, 8)
			d.nameDocs[k] = m
		}
		m[id] = struct{}{}
		d.nameTerms[k] = struct{}{}
	}
	if r.Kind != "" {
		k := strings.ToLower(r.Kind)
		m := d.kindDocs[k]
		if m == nil {
			m = make(map[uint32]struct{}, 8)
			d.kindDocs[k] = m
		}
		m[id] = struct{}{}
		d.kindTerms[k] = struct{}{}
	}
}

func removeFieldMapsDataNoBKT(d *IndexData, id uint32, r SearchableResource) {
	if r.Name != "" {
		k := strings.ToLower(r.Name)
		if m := d.nameDocs[k]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(d.nameDocs, k)
			}
		}
	}
	if r.Kind != "" {
		k := strings.ToLower(r.Kind)
		if m := d.kindDocs[k]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(d.kindDocs, k)
			}
		}
	}
}

func addFieldMapsData(d *IndexData, id uint32, r SearchableResource) {
	if r.Name != "" {
		k := strings.ToLower(r.Name)
		m := d.nameDocs[k]
		if m == nil {
			m = make(map[uint32]struct{}, 8)
			d.nameDocs[k] = m
		}
		m[id] = struct{}{}
		if _, ok := d.nameTerms[k]; !ok {
			d.bktName.Insert(k)
			d.nameTerms[k] = struct{}{}
		}
	}
	if r.Kind != "" {
		k := strings.ToLower(r.Kind)
		m := d.kindDocs[k]
		if m == nil {
			m = make(map[uint32]struct{}, 8)
			d.kindDocs[k] = m
		}
		m[id] = struct{}{}
		if _, ok := d.kindTerms[k]; !ok {
			d.bktKind.Insert(k)
			d.kindTerms[k] = struct{}{}
		}
	}
}

func removeFieldMapsData(d *IndexData, id uint32, r SearchableResource) {
	if r.Name != "" {
		k := strings.ToLower(r.Name)
		if m := d.nameDocs[k]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(d.nameDocs, k)
			}
		}
	}
	if r.Kind != "" {
		k := strings.ToLower(r.Kind)
		if m := d.kindDocs[k]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(d.kindDocs, k)
			}
		}
	}
}

func removeFromInvertedIndexFastData(d *IndexData, docID uint32) {
	if termIndices, ok := d.docTerms[docID]; ok {
		d.invertedIdx.RemoveDoc(docID, termIndices)
		delete(d.docTerms, docID)
	}
}

// PrepareResource exports the prepare function for parallel use
func PrepareResource(r SearchableResource) PreparedResource {
	return prepareResource(r)
}

// BatchRemove removes multiple resources with a single lock acquisition
func (idx *MemoryIndex) BatchRemove(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	d := idx.getData()

	for _, id := range ids {
		if docID, ok := d.pools.IDs.Lookup(id); ok {
			idx.removeDocLocked(d, docID)
		}
	}
	idx.updateStatsLocked(d)
	return nil
}

// Clear removes all documents from the index
func (idx *MemoryIndex) Clear() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.data.Store(newIndexData())
	idx.stats = IndexStats{LastUpdated: time.Now()}
	return nil
}

// SwapData atomically replaces the entire index data (for bulk loading)
func (idx *MemoryIndex) SwapData(newData *IndexData) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.data.Store(newData)
	idx.updateStatsLocked(newData)
}

// NewIndexData creates a new empty indexData for bulk loading
func NewIndexData() *IndexData {
	return newIndexData()
}

func (idx *MemoryIndex) needsCompaction() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d := idx.getData()
	return idx.removedSinceRebuild > compactRemovedThreshold ||
		d.invertedIdx.TermCount() > compactTermRatio*len(d.compactDocs)+compactTermSlack
}

func (idx *MemoryIndex) MaybeCompact() bool {
	if !idx.needsCompaction() {
		return false
	}
	idx.CompactNow()
	return true
}

// CompactNow rebuilds the index from its live documents, discarding every term,
// interned string, and BK-tree node that only dead documents referenced. The
// data pointer swap keeps in-flight readers valid on the old snapshot.
func (idx *MemoryIndex) CompactNow() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	d := idx.getData()
	type agedDoc struct {
		res SearchableResource
		age int64
	}
	docs := make([]agedDoc, 0, len(d.compactDocs))
	for id, compact := range d.compactDocs {
		docs = append(docs, agedDoc{compact.ToSearchable(d.pools), d.accessTimes[id]})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].age < docs[j].age })
	prepared := make([]PreparedResource, len(docs))
	workers := 4
	if len(docs) < workers*32 {
		workers = 1
	}
	chunk := (len(docs) + workers - 1) / workers
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo, hi := w*chunk, (w+1)*chunk
		if hi > len(docs) {
			hi = len(docs)
		}
		if lo >= hi {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				prepared[i] = prepareResource(docs[i].res)
			}
		}(lo, hi)
	}
	wg.Wait()
	nd := newIndexDataWithCapacity(len(prepared))
	BatchIndexToDataFast(nd, prepared)
	FinalizeBulkLoad(nd)
	idx.data.Store(nd)
	if idx.warmTier != nil {
		idx.warmTier.Clear()
	}
	idx.removedSinceRebuild = 0
	idx.updateStatsLocked(nd)
}

func (idx *MemoryIndex) RemoveCluster(cluster string) int {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	d := idx.getData()
	var victims []uint32
	for id, compact := range d.compactDocs {
		if d.pools.Clusters.Get(compact.Cluster) == cluster {
			victims = append(victims, id)
		}
	}
	for _, id := range victims {
		idx.removeDocLocked(d, id)
	}
	if len(victims) > 0 {
		idx.updateStatsLocked(d)
	}
	return len(victims)
}

// Stats returns index statistics
func (idx *MemoryIndex) Stats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.stats
}

// HasDocument checks if a document exists in the memory index
func (idx *MemoryIndex) HasDocument(id string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d := idx.getData()
	docID, ok := d.pools.IDs.Lookup(id)
	if !ok {
		return false
	}
	_, exists := d.compactDocs[docID]
	return exists
}

// GetDocument returns a document from the memory index if it exists
func (idx *MemoryIndex) GetDocument(id string) (SearchableResource, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d := idx.getData()
	return d.getDocumentByStringID(id)
}

// GetDocumentWithWarmTier returns a document from memory or warm tier
func (idx *MemoryIndex) GetDocumentWithWarmTier(id string) (SearchableResource, bool) {
	idx.mu.RLock()
	d := idx.getData()
	if doc, ok := d.getDocumentByStringID(id); ok {
		idx.mu.RUnlock()
		return doc, true
	}
	wt := idx.warmTier
	if wt == nil {
		idx.mu.RUnlock()
		return SearchableResource{}, false
	}
	docID, ok := d.pools.IDs.Lookup(id)
	pools := d.pools
	idx.mu.RUnlock()
	if !ok {
		return SearchableResource{}, false
	}
	if compact, ok := wt.Get(docID); ok {
		return compact.ToSearchable(pools), true
	}
	return SearchableResource{}, false
}

// DocumentCount returns the number of documents in the memory index
func (idx *MemoryIndex) DocumentCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d := idx.getData()
	return len(d.compactDocs)
}

// Helper methods

// Helpers for plural/singular normalization and uniqueness

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (idx *MemoryIndex) removeFromInvertedIndexFastLocked(d *IndexData, docID uint32) {
	if termIndices, ok := d.docTerms[docID]; ok {
		d.invertedIdx.RemoveDoc(docID, termIndices)
		delete(d.docTerms, docID)
	}
}

func (idx *MemoryIndex) matchesFiltersCompact(d *IndexData, compact CompactResource, query SearchQuery) bool {
	if query.Cluster != "" && d.pools.Clusters.Get(compact.Cluster) != query.Cluster {
		return false
	}
	if len(query.Clusters) > 0 && !containsString(query.Clusters, d.pools.Clusters.Get(compact.Cluster)) {
		return false
	}
	if len(query.Namespaces) > 0 {
		ns := d.pools.Namespaces.Get(compact.Namespace)
		found := false
		for _, qns := range query.Namespaces {
			if ns == qns {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(query.Kinds) > 0 {
		kind := d.pools.Kinds.Get(compact.Kind)
		found := false
		for _, qkind := range query.Kinds {
			if strings.EqualFold(kind, qkind) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (idx *MemoryIndex) matchesFiltersData(d *IndexData, resource SearchableResource, query SearchQuery) bool {
	if query.Cluster != "" && resource.Cluster != query.Cluster {
		return false
	}
	if len(query.Clusters) > 0 && !containsString(query.Clusters, resource.Cluster) {
		return false
	}
	if len(query.Namespaces) > 0 {
		found := false
		for _, ns := range query.Namespaces {
			if resource.Namespace == ns {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(query.Kinds) > 0 {
		found := false
		for _, kind := range query.Kinds {
			if strings.EqualFold(resource.Kind, kind) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for key, value := range query.Labels {
		if resource.Labels[key] != value {
			return false
		}
	}
	return true
}

func (idx *MemoryIndex) findMatch(resource SearchableResource, token string) *SearchMatch {
	// Check name
	if strings.Contains(strings.ToLower(resource.Name), token) {
		return &SearchMatch{
			Field: "name",
			Value: resource.Name,
		}
	}

	// Check namespace
	if strings.Contains(strings.ToLower(resource.Namespace), token) {
		return &SearchMatch{
			Field: "namespace",
			Value: resource.Namespace,
		}
	}

	// Check kind
	if strings.Contains(strings.ToLower(resource.Kind), token) {
		return &SearchMatch{
			Field: "kind",
			Value: resource.Kind,
		}
	}

	return nil
}

func (idx *MemoryIndex) updateStatsLocked(d *IndexData) {
	clusterList := make([]string, 0, len(d.clusterDocs))
	for clusterID := range d.clusterDocs {
		clusterList = append(clusterList, d.pools.Clusters.Get(clusterID))
	}
	idx.stats = IndexStats{
		TotalDocuments: len(d.compactDocs),
		IndexSize:      int64(d.invertedIdx.TermCount()),
		LastUpdated:    time.Now(),
		Clusters:       clusterList,
	}
}

func (idx *MemoryIndex) updateStatsIncrementalLocked(d *IndexData) {
	idx.stats.TotalDocuments = len(d.compactDocs)
	idx.stats.IndexSize = int64(d.invertedIdx.TermCount())
	idx.stats.LastUpdated = time.Now()
}

// tokenize converts a string into searchable tokens with Kubernetes-aware parsing
func tokenize(text string) []string {
	original := text
	text = strings.ToLower(text)
	parts := tokenizeRegex.Split(text, -1)
	tokens := make([]string, 0, len(parts)*3)
	if len(original) > 0 {
		tokens = append(tokens, strings.ToLower(original))
	}
	for _, sp := range splitCamelCaseAdvanced(original) {
		if len(sp) >= 2 {
			tokens = append(tokens, sp)
		}
	}
	for _, part := range parts {
		if part == "" || len(part) < 2 {
			continue
		}
		tokens = append(tokens, part)
		subparts := splitCamelCaseAdvanced(part)
		tokens = append(tokens, subparts...)
	}
	seen := make(map[string]bool, len(tokens))
	unique := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if len(token) >= 1 && len(token) <= maxTermLen && !seen[token] {
			seen[token] = true
			unique = append(unique, token)
		}
	}
	return unique
}

// prefixTokens returns every 2..6 char prefix of each whole word in text. Used
// only for high-value fields (resource Name and Kind) so a partial query like
// "depl" lands directly on a posting list in the candidate stage. We no longer
// emit these for every field inside tokenize(): doing so multiplied the term
// count ~5x — mostly across high-cardinality label/annotation values that
// nobody prefix-searches — and was the dominant driver of search-index RAM.
// Prefix and substring matching against other fields still happens at scoring
// time (calculatePrefixScore/calculateSubstringScore), so coverage is intact.
func prefixTokens(text string) []string {
	parts := tokenizeRegex.Split(strings.ToLower(text), -1)
	seen := make(map[string]bool)
	var out []string
	for _, part := range parts {
		for i := 2; i <= len(part) && i <= 6; i++ {
			p := part[:i]
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// splitCamelCaseAdvanced splits camelCase words with better handling of abbreviations
func splitCamelCaseAdvanced(s string) []string {
	if len(s) <= 1 {
		return []string{s}
	}

	result := make([]string, 0, 4)
	start := 0
	inUpper := false

	for i := 0; i < len(s); i++ {
		isUpper := unicode.IsUpper(rune(s[i]))

		if i > 0 && isUpper && !inUpper {
			if i > start {
				part := strings.ToLower(s[start:i])
				if len(part) >= 2 || abbreviations[part] {
					result = append(result, part)
				}
			}
			start = i
		}

		if isUpper && i+1 < len(s) && !unicode.IsUpper(rune(s[i+1])) {
			if i > start {
				part := strings.ToLower(s[start:i])
				if len(part) >= 2 || abbreviations[part] {
					result = append(result, part)
				}
				start = i
			}
			inUpper = false
		} else {
			inUpper = isUpper
		}
	}

	if start < len(s) {
		part := strings.ToLower(s[start:])
		if len(part) >= 2 || abbreviations[part] {
			result = append(result, part)
		}
	}

	return result
}

// calculateSimilarity calculates string similarity using Levenshtein distance
func levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}
	if s1 == s2 {
		return 0
	}
	if len(s1) > len(s2) {
		s1, s2 = s2, s1
	}
	n := len(s2) + 1
	buf := levBufferPool.Get().([]int)
	if len(buf) < n*2 {
		buf = make([]int, n*2)
	}
	prev, curr := buf[:n], buf[n:n*2]
	for j := 0; j < n; j++ {
		prev[j] = j
	}
	for i := 1; i <= len(s1); i++ {
		curr[0] = i
		for j := 1; j <= len(s2); j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	result := prev[len(s2)]
	if shouldPoolLevBuffer(len(buf)) {
		levBufferPool.Put(buf)
	}
	return result
}

func levenshteinBounded(s1, s2 string, maxD int) int {
	if s1 == s2 {
		return 0
	}
	if len(s1) > len(s2) {
		s1, s2 = s2, s1
	}
	if len(s2)-len(s1) > maxD {
		return maxD + 1
	}
	if len(s1) == 0 {
		return len(s2)
	}
	n := len(s2) + 1
	buf := levBufferPool.Get().([]int)
	if len(buf) < n*2 {
		buf = make([]int, n*2)
	}
	prev, curr := buf[:n], buf[n:n*2]
	for j := 0; j < n; j++ {
		prev[j] = j
	}
	result := maxD + 1
	for i := 1; i <= len(s1); i++ {
		curr[0] = i
		rowMin := i
		for j := 1; j <= len(s2); j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > maxD {
			if shouldPoolLevBuffer(len(buf)) {
				levBufferPool.Put(buf)
			}
			return maxD + 1
		}
		prev, curr = curr, prev
	}
	if prev[len(s2)] <= maxD {
		result = prev[len(s2)]
	}
	if shouldPoolLevBuffer(len(buf)) {
		levBufferPool.Put(buf)
	}
	return result
}

func fuzzySimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}
	if maxLen == 0 {
		return 0.0
	}
	maxD := (2*maxLen - 1) / 5
	d := levenshteinBounded(s1, s2, maxD)
	if d > maxD {
		return 0.0
	}
	return 1.0 - float64(d)/float64(maxLen)
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// calculateExactScore gives highest score for exact matches
func (idx *MemoryIndex) calculateExactScore(resource SearchableResource, query string) (float64, []SearchMatch) {
	var matches []SearchMatch
	score := 0.0

	// Exact name match (highest priority)
	if strings.ToLower(resource.Name) == query {
		matches = append(matches, SearchMatch{Field: "name", Value: resource.Name})
		score += 100.0
	}

	// Exact kind match (strongly emphasized)
	if strings.ToLower(resource.Kind) == query {
		matches = append(matches, SearchMatch{Field: "kind", Value: resource.Kind})
		score += 120.0
	}

	// Exact namespace match
	if strings.ToLower(resource.Namespace) == query {
		matches = append(matches, SearchMatch{Field: "namespace", Value: resource.Namespace})
		score += 60.0
	}

	// Exact label key/value matches
	for key, value := range resource.Labels {
		if strings.ToLower(key) == query || strings.ToLower(value) == query {
			matches = append(matches, SearchMatch{Field: "labels", Value: fmt.Sprintf("%s=%s", key, value)})
			score += 40.0
		}
	}

	return score, matches
}

// calculatePrefixScore gives high score for prefix matches
func (idx *MemoryIndex) calculatePrefixScore(resource SearchableResource, query string) (float64, []SearchMatch) {
	var matches []SearchMatch
	score := 0.0

	// Name prefix match
	if strings.HasPrefix(strings.ToLower(resource.Name), query) && len(query) >= 2 {
		matches = append(matches, SearchMatch{Field: "name", Value: resource.Name})
		score += 70.0 * (float64(len(query)) / float64(len(resource.Name)))
	}

	// Kind prefix match (stronger)
	if strings.HasPrefix(strings.ToLower(resource.Kind), query) && len(query) >= 2 {
		matches = append(matches, SearchMatch{Field: "kind", Value: resource.Kind})
		score += 80.0 * (float64(len(query)) / float64(len(resource.Kind)))
	}

	// Namespace prefix match
	if strings.HasPrefix(strings.ToLower(resource.Namespace), query) && len(query) >= 2 {
		matches = append(matches, SearchMatch{Field: "namespace", Value: resource.Namespace})
		score += 30.0 * (float64(len(query)) / float64(len(resource.Namespace)))
	}

	return score, matches
}

// calculateSubstringScore gives medium score for substring matches
func (idx *MemoryIndex) calculateSubstringScore(resource SearchableResource, query string) (float64, []SearchMatch) {
	var matches []SearchMatch
	score := 0.0

	if len(query) < 2 {
		return score, matches
	}

	// Name substring match
	if strings.Contains(strings.ToLower(resource.Name), query) {
		matches = append(matches, SearchMatch{Field: "name", Value: resource.Name})
		score += 30.0 * (float64(len(query)) / float64(len(resource.Name)))
	}

	// Kind substring match (stronger)
	if strings.Contains(strings.ToLower(resource.Kind), query) {
		matches = append(matches, SearchMatch{Field: "kind", Value: resource.Kind})
		score += 45.0 * (float64(len(query)) / float64(len(resource.Kind)))
	}

	// Namespace substring match
	if strings.Contains(strings.ToLower(resource.Namespace), query) {
		matches = append(matches, SearchMatch{Field: "namespace", Value: resource.Namespace})
		score += 20.0 * (float64(len(query)) / float64(len(resource.Namespace)))
	}

	// Description substring match
	if strings.Contains(strings.ToLower(resource.Description), query) {
		matches = append(matches, SearchMatch{Field: "description", Value: resource.Description})
		score += 15.0
	}

	// Label values substring match
	for key, value := range resource.Labels {
		if strings.Contains(strings.ToLower(key), query) || strings.Contains(strings.ToLower(value), query) {
			matches = append(matches, SearchMatch{Field: "labels", Value: fmt.Sprintf("%s=%s", key, value)})
			score += 10.0
		}
	}

	return score, matches
}

// calculateFuzzyScore gives low score for fuzzy matches
func (idx *MemoryIndex) calculateFuzzyScore(resource SearchableResource, query string) (float64, []SearchMatch) {
	var matches []SearchMatch
	score := 0.0

	if len(query) < 3 {
		return score, matches
	}

	// Fuzzy name match
	if similarity := fuzzySimilarity(query, strings.ToLower(resource.Name)); similarity > 0.6 {
		matches = append(matches, SearchMatch{Field: "name", Value: resource.Name})
		score += 20.0 * similarity
	}

	// Fuzzy kind match (stronger)
	if similarity := fuzzySimilarity(query, strings.ToLower(resource.Kind)); similarity > 0.6 {
		matches = append(matches, SearchMatch{Field: "kind", Value: resource.Kind})
		score += 30.0 * similarity
	}

	// Fuzzy namespace match
	if len(resource.Namespace) > 0 {
		if similarity := fuzzySimilarity(query, strings.ToLower(resource.Namespace)); similarity > 0.6 {
			matches = append(matches, SearchMatch{Field: "namespace", Value: resource.Namespace})
			score += 10.0 * similarity
		}
	}

	return score, matches
}

// getResourceBoost applies importance-based scoring boost
func (idx *MemoryIndex) getResourceBoost(resource SearchableResource) float64 {
	boost := 1.0

	// Boost based on resource type importance
	switch strings.ToLower(resource.Kind) {
	case "pod", "deployment", "service":
		boost *= 1.3
	case "ingress", "configmap", "secret":
		boost *= 1.2
	case "job", "cronjob", "statefulset", "daemonset":
		boost *= 1.1
	}

	// Boost more recent resources slightly
	age := time.Since(resource.UpdatedAt)
	if age < 24*time.Hour {
		boost *= 1.1
	} else if age < 7*24*time.Hour {
		boost *= 1.05
	}

	return boost
}

// deduplicateMatches removes duplicate matches
func (idx *MemoryIndex) deduplicateMatches(matches []SearchMatch) []SearchMatch {
	seen := make(map[string]bool)
	result := []SearchMatch{}

	for _, match := range matches {
		key := match.Field + ":" + match.Value
		if !seen[key] {
			seen[key] = true
			result = append(result, match)
		}
	}

	return result
}
