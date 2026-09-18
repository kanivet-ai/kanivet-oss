package storage

import "sync"

// BulkLoader rebuilds every shard from persisted documents in one pass.
// Callers feed prepared resources as they stream out of the database; each
// one lands directly in the working IndexData of its cluster's shard, so a
// document is tokenized exactly once. Finish builds the fuzzy-search trees
// and swaps every shard's data in.
//
// This replaces the old two-step load, which built one index holding every
// document and then re-read and re-tokenized all of it to split it into
// shards. On a database with close to a million rows that second pass alone
// allocated over ten gigabytes.
type BulkLoader struct {
	s     *ShardedIndex
	data  []*IndexData
	added int
}

// NewBulkLoader sizes each shard's working set from capacityHint, the number
// of documents the caller expects to add across all shards.
func (s *ShardedIndex) NewBulkLoader(capacityHint int) *BulkLoader {
	per := capacityHint/len(s.shards) + 1
	data := make([]*IndexData, len(s.shards))
	for i := range data {
		data[i] = newIndexDataWithCapacity(per)
	}
	return &BulkLoader{s: s, data: data}
}

// Add routes each prepared resource to the pending data of its cluster's
// shard, using the same hash as live indexing so lookups by ID keep working.
func (b *BulkLoader) Add(prepared []PreparedResource) {
	if len(prepared) == 0 {
		return
	}
	b.added += len(prepared)
	if len(b.data) == 1 {
		BatchIndexToDataFast(b.data[0], prepared)
		return
	}
	groups := make(map[uint32][]PreparedResource, len(b.data))
	for _, p := range prepared {
		idx := b.s.shardIndex(p.Resource.Cluster)
		groups[idx] = append(groups[idx], p)
	}
	for idx, group := range groups {
		BatchIndexToDataFast(b.data[idx], group)
	}
}

// Added reports how many prepared resources have been fed to the loader.
func (b *BulkLoader) Added() int { return b.added }

// Finish finalizes each shard's data and swaps it in. Shards are independent,
// so their BK-trees are built concurrently; the swaps themselves are atomic
// per shard, and readers keep seeing the previous snapshot until then.
func (b *BulkLoader) Finish() {
	var wg sync.WaitGroup
	for _, d := range b.data {
		wg.Add(1)
		go func(d *IndexData) {
			defer wg.Done()
			FinalizeBulkLoad(d)
		}(d)
	}
	wg.Wait()
	for i, d := range b.data {
		b.s.shards[i].SwapData(d)
	}
}
