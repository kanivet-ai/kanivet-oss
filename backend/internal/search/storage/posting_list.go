package storage

import "sort"

type PostingEntry struct {
	DocID uint32
	Score float32
}

type PostingList struct {
	entries    []PostingEntry
	deleted    []bool
	unsorted   bool
	tombstones int
}

func NewPostingList() *PostingList {
	return &PostingList{entries: make([]PostingEntry, 0, 8)}
}

func (p *PostingList) Add(docID uint32, score float32) {
	idx := sort.Search(len(p.entries), func(i int) bool {
		return p.entries[i].DocID >= docID
	})
	if idx < len(p.entries) && p.entries[idx].DocID == docID {
		if p.deleted != nil && p.deleted[idx] {
			// Resurrect a previously-tombstoned slot.
			p.deleted[idx] = false
			p.entries[idx].Score = score
			p.tombstones--
			return
		}
		p.entries[idx].Score += score
		return
	}
	p.entries = append(p.entries, PostingEntry{})
	copy(p.entries[idx+1:], p.entries[idx:])
	p.entries[idx] = PostingEntry{DocID: docID, Score: score}
	if p.deleted != nil {
		p.deleted = append(p.deleted, false)
		copy(p.deleted[idx+1:], p.deleted[idx:])
		p.deleted[idx] = false
	}
}

// Remove logically deletes docID without paying O(N) to shift elements.
// We keep a parallel "deleted" bitmap; the entries slice stays sorted by DocID
// so binary search continues to work. Compaction is triggered lazily when
// the deleted-ratio gets high enough.
func (p *PostingList) Remove(docID uint32) bool {
	liveLen := len(p.entries) - p.tombstones
	if liveLen == 0 {
		return false
	}
	// Binary search ignores tombstones: tombstones live at slots whose DocID
	// in p.entries is still the original ID, but they're marked in p.deleted.
	// We need to search across all entries that aren't tombstoned. Cheapest:
	// binary search the full slice (it's still sorted by DocID) and verify
	// the hit isn't deleted.
	idx := sort.Search(len(p.entries), func(i int) bool {
		return p.entries[i].DocID >= docID
	})
	if idx < len(p.entries) && p.entries[idx].DocID == docID {
		if p.deleted != nil && p.deleted[idx] {
			return false // already removed
		}
		if p.deleted == nil {
			p.deleted = make([]bool, len(p.entries))
		}
		p.deleted[idx] = true
		p.tombstones++
		if p.shouldCompact() {
			p.compact()
		}
		return true
	}
	return false
}

func (p *PostingList) shouldCompact() bool {
	// Compact when tombstones >= 25% of total or >= 64 absolute.
	return p.tombstones >= 64 && p.tombstones*4 >= len(p.entries)
}

func (p *PostingList) compact() {
	if p.tombstones == 0 || p.deleted == nil {
		return
	}
	w := 0
	for i, e := range p.entries {
		if p.deleted[i] {
			continue
		}
		p.entries[w] = e
		w++
	}
	p.entries = p.entries[:w]
	p.deleted = nil
	p.tombstones = 0
}

// Compact forces tombstone cleanup; safe to call externally.
func (p *PostingList) Compact() { p.compact() }

func (p *PostingList) Get(docID uint32) (float32, bool) {
	idx := sort.Search(len(p.entries), func(i int) bool {
		return p.entries[i].DocID >= docID
	})
	if idx < len(p.entries) && p.entries[idx].DocID == docID {
		if p.deleted != nil && p.deleted[idx] {
			return 0, false
		}
		return p.entries[idx].Score, true
	}
	return 0, false
}

func (p *PostingList) Len() int {
	return len(p.entries) - p.tombstones
}

func (p *PostingList) IsEmpty() bool {
	return p.Len() == 0
}

func (p *PostingList) Iterate(fn func(docID uint32, score float32)) {
	for i, e := range p.entries {
		if p.deleted != nil && p.deleted[i] {
			continue
		}
		fn(e.DocID, e.Score)
	}
}

func (p *PostingList) DocIDs() []uint32 {
	live := len(p.entries) - p.tombstones
	ids := make([]uint32, 0, live)
	for i, e := range p.entries {
		if p.deleted != nil && p.deleted[i] {
			continue
		}
		ids = append(ids, e.DocID)
	}
	return ids
}

func (p *PostingList) AddUnsorted(docID uint32, score float32) {
	p.entries = append(p.entries, PostingEntry{DocID: docID, Score: score})
	p.unsorted = true
}

func (p *PostingList) Finalize() {
	if p.tombstones > 0 {
		p.compact()
	}
	if !p.unsorted || len(p.entries) == 0 {
		return
	}
	sort.Slice(p.entries, func(i, j int) bool { return p.entries[i].DocID < p.entries[j].DocID })
	w := 0
	for r := 1; r < len(p.entries); r++ {
		if p.entries[r].DocID == p.entries[w].DocID {
			p.entries[w].Score += p.entries[r].Score
		} else {
			w++
			p.entries[w] = p.entries[r]
		}
	}
	p.entries = p.entries[:w+1]
	p.unsorted = false
}

type CompactInvertedIndex struct {
	terms    []string
	termIdx  map[string]uint32
	postings []*PostingList
}

func NewCompactInvertedIndex() *CompactInvertedIndex {
	return &CompactInvertedIndex{
		terms:    make([]string, 0, 10000),
		termIdx:  make(map[string]uint32, 10000),
		postings: make([]*PostingList, 0, 10000),
	}
}

func (c *CompactInvertedIndex) getOrCreateTerm(term string) uint32 {
	if idx, ok := c.termIdx[term]; ok {
		return idx
	}
	idx := uint32(len(c.terms))
	c.terms = append(c.terms, term)
	c.termIdx[term] = idx
	c.postings = append(c.postings, NewPostingList())
	return idx
}

func (c *CompactInvertedIndex) Add(term string, docID uint32, score float32) {
	idx := c.getOrCreateTerm(term)
	c.postings[idx].Add(docID, score)
}

func (c *CompactInvertedIndex) AddAndGetIndex(term string, docID uint32, score float32) uint32 {
	idx := c.getOrCreateTerm(term)
	c.postings[idx].Add(docID, score)
	return idx
}

func (c *CompactInvertedIndex) Remove(term string, docID uint32) {
	if idx, ok := c.termIdx[term]; ok {
		c.postings[idx].Remove(docID)
	}
}

func (c *CompactInvertedIndex) GetPostingList(term string) *PostingList {
	if idx, ok := c.termIdx[term]; ok {
		return c.postings[idx]
	}
	return nil
}

func (c *CompactInvertedIndex) HasTerm(term string) bool {
	_, ok := c.termIdx[term]
	return ok
}

func (c *CompactInvertedIndex) TermCount() int {
	return len(c.terms)
}

func (c *CompactInvertedIndex) RemoveDoc(docID uint32, termIndices []uint32) {
	if len(termIndices) > 0 {
		for _, termIdx := range termIndices {
			if int(termIdx) < len(c.postings) {
				c.postings[termIdx].Remove(docID)
			}
		}
	} else {
		c.RemoveDocFromAll(docID)
	}
}

func (c *CompactInvertedIndex) RemoveDocFromAll(docID uint32) {
	for _, posting := range c.postings {
		posting.Remove(docID)
	}
}

func (c *CompactInvertedIndex) GetTermIndex(term string) (uint32, bool) {
	idx, ok := c.termIdx[term]
	return idx, ok
}

func (c *CompactInvertedIndex) GetTerm(idx uint32) string {
	if int(idx) < len(c.terms) {
		return c.terms[idx]
	}
	return ""
}

func (c *CompactInvertedIndex) AddUnsorted(term string, docID uint32, score float32) uint32 {
	idx := c.getOrCreateTerm(term)
	c.postings[idx].AddUnsorted(docID, score)
	return idx
}

func (c *CompactInvertedIndex) FinalizeAll() {
	for _, p := range c.postings {
		p.Finalize()
	}
}
