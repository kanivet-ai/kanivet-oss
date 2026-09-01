package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenizeSkipsOversizedTerms(t *testing.T) {
	blob := strings.Repeat("x", 5000)
	for _, tok := range tokenize(blob) {
		if len(tok) > maxTermLen {
			t.Fatalf("tokenize emitted %d-char term", len(tok))
		}
	}
	for _, tok := range tokenize("web-" + strings.Repeat("a", 100) + "-pod") {
		if len(tok) > maxTermLen {
			t.Fatalf("tokenize emitted %d-char term from mixed input", len(tok))
		}
	}
	found := false
	for _, tok := range tokenize("nginx-ingress") {
		if tok == "nginx-ingress" {
			found = true
		}
	}
	if !found {
		t.Fatal("tokenize dropped normal full-string token")
	}
}

func TestBuildTokensSkipsGarbageAnnotations(t *testing.T) {
	manifest := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"web"},"spec":` + strings.Repeat(`{"a":"b"},`, 200) + `}`
	r := testResource("c", "apps", "v1", "Deployment", "ns", "web")
	r.Annotations = map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": manifest,
		"checksum/config":             "abc123def456",
		"kapp.k14s.io/original":       `{"big":"blob"}`,
		"banzaicloud.io/last-applied": `{"big":"blob"}`,
		"owner":                       "team-x",
		"longvalue":                   strings.Repeat("z", 300),
	}
	tokens := buildTokens(r)
	defer putTokens(tokens)
	for tok := range tokens {
		if strings.Contains(tok, "apiversion") || tok == "abc123def456" || strings.Contains(tok, "zzzzzzzzzz") {
			t.Fatalf("garbage annotation value tokenized: %q", tok)
		}
	}
	if _, ok := tokens["team"]; !ok {
		t.Fatal("benign annotation value not tokenized")
	}
	if _, ok := tokens["checksum"]; !ok {
		t.Fatal("annotation key not tokenized")
	}
}

func TestPoolGuards(t *testing.T) {
	if shouldPoolTokens(5000) {
		t.Fatal("oversized tokens map must not be pooled")
	}
	if !shouldPoolTokens(64) {
		t.Fatal("normal tokens map must be pooled")
	}
	if shouldPoolLevBuffer(10000) {
		t.Fatal("oversized lev buffer must not be pooled")
	}
	if !shouldPoolLevBuffer(512) {
		t.Fatal("normal lev buffer must be pooled")
	}
}

func TestInternPoolLookupDoesNotGrow(t *testing.T) {
	p := NewStringInternPool()
	p.Intern("present")
	size := p.Size()
	if _, ok := p.Lookup("absent"); ok {
		t.Fatal("Lookup found absent string")
	}
	if idx, ok := p.Lookup("present"); !ok || p.Get(idx) != "present" {
		t.Fatal("Lookup missed present string")
	}
	if p.Size() != size {
		t.Fatalf("Lookup grew pool: %d -> %d", size, p.Size())
	}
}

func TestReadPathsDoNotIntern(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("c", "apps", "v1", "Deployment", "ns", "real")); err != nil {
		t.Fatal(err)
	}
	d := idx.getData()
	before := d.pools.IDs.Size()
	if idx.HasDocument("c/apps/v1/deployments/ns/ghost") {
		t.Fatal("ghost doc reported present")
	}
	if _, ok := idx.GetDocument("c/apps/v1/deployments/ns/ghost2"); ok {
		t.Fatal("ghost doc returned")
	}
	if _, ok := idx.GetDocumentWithWarmTier("c/apps/v1/deployments/ns/ghost3"); ok {
		t.Fatal("ghost doc returned from warm tier path")
	}
	if err := idx.Remove("c/apps/v1/deployments/ns/ghost4"); err == nil {
		t.Fatal("removing ghost doc must error")
	}
	if err := idx.BatchRemove([]string{"c/apps/v1/deployments/ns/ghost5"}); err != nil {
		t.Fatal(err)
	}
	if got := d.pools.IDs.Size(); got != before {
		t.Fatalf("read paths grew IDs pool: %d -> %d", before, got)
	}
}

func TestGetDocumentWithWarmTierFallsBackToWarmTier(t *testing.T) {
	idx := NewMemoryIndex()
	wt, err := NewWarmTier(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := wt.Close(); err != nil {
			t.Fatalf("close warm tier: %v", err)
		}
	})
	idx.SetWarmTier(wt)

	r := testResource("c", "apps", "v1", "Deployment", "ns", "warm-only")
	if err := idx.Index(r); err != nil {
		t.Fatal(err)
	}

	idx.mu.Lock()
	d := idx.getData()
	docID, ok := d.pools.IDs.Lookup(r.ID)
	if !ok {
		idx.mu.Unlock()
		t.Fatalf("missing doc id for %s", r.ID)
	}
	compact, ok := d.compactDocs[docID]
	if !ok {
		idx.mu.Unlock()
		t.Fatalf("missing compact doc for %s", r.ID)
	}
	if err := wt.Store(compact); err != nil {
		idx.mu.Unlock()
		t.Fatal(err)
	}
	if !idx.removeDocLocked(d, docID) {
		idx.mu.Unlock()
		t.Fatalf("remove warm-only doc %s", r.ID)
	}
	idx.mu.Unlock()

	if _, ok := idx.GetDocument(r.ID); ok {
		t.Fatalf("document %s still present in memory index", r.ID)
	}

	got, ok := idx.GetDocumentWithWarmTier(r.ID)
	if !ok {
		t.Fatalf("expected warm-tier fallback for %s", r.ID)
	}
	if got.ID != r.ID || got.Cluster != r.Cluster || got.Kind != r.Kind || got.Namespace != r.Namespace || got.Name != r.Name || got.Group != r.Group || got.Version != r.Version || got.Category != r.Category {
		t.Fatalf("unexpected warm-tier document: %#v", got)
	}
	if got.UpdatedAt.Unix() != r.UpdatedAt.Unix() {
		t.Fatalf("unexpected UpdatedAt: got=%d want=%d", got.UpdatedAt.Unix(), r.UpdatedAt.Unix())
	}
}

func TestCompactNowReclaimsTermTable(t *testing.T) {
	idx := NewMemoryIndex()
	for i := 0; i < 2000; i++ {
		r := testResource("c", "batch", "v1", "Job", "ns", fmt.Sprintf("job-%04d-abc%04d", i, i))
		if err := idx.Index(r); err != nil {
			t.Fatal(err)
		}
	}
	var keepIDs []string
	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("c/batch/v1/job/ns/job-%04d-abc%04d", i, i)
		if i >= 1900 {
			keepIDs = append(keepIDs, id)
			continue
		}
		if err := idx.Remove(id); err != nil {
			t.Fatalf("remove %s: %v", id, err)
		}
	}
	termsBefore := idx.getData().invertedIdx.TermCount()
	namesBefore := idx.getData().pools.Names.Size()
	idx.CompactNow()
	d := idx.getData()
	if d.invertedIdx.TermCount() >= termsBefore/2 {
		t.Fatalf("terms not reclaimed: %d -> %d", termsBefore, d.invertedIdx.TermCount())
	}
	if d.pools.Names.Size() >= namesBefore/2 {
		t.Fatalf("names pool not reclaimed: %d -> %d", namesBefore, d.pools.Names.Size())
	}
	if idx.DocumentCount() != 100 {
		t.Fatalf("compact changed doc count: %d", idx.DocumentCount())
	}
	for _, id := range keepIDs {
		if !idx.HasDocument(id) {
			t.Fatalf("survivor %s lost in compaction", id)
		}
	}
	res, err := idx.Search(SearchQuery{Text: "job-1950-abc1950", Limit: 5})
	if err != nil || len(res) == 0 {
		t.Fatalf("survivor unsearchable after compaction: %v (%d results)", err, len(res))
	}
}

func TestNeedsCompactionThresholds(t *testing.T) {
	idx := NewMemoryIndex()
	if idx.needsCompaction() {
		t.Fatal("empty index wants compaction")
	}
	idx.mu.Lock()
	idx.removedSinceRebuild = compactRemovedThreshold + 1
	idx.mu.Unlock()
	if !idx.needsCompaction() {
		t.Fatal("removal threshold not honored")
	}
	if !idx.MaybeCompact() {
		t.Fatal("MaybeCompact should run when needed")
	}
	if idx.needsCompaction() {
		t.Fatal("compaction did not reset counters")
	}
	if idx.MaybeCompact() {
		t.Fatal("MaybeCompact should be a no-op when not needed")
	}
}

func TestShardedMaybeCompactAll(t *testing.T) {
	s := NewShardedIndex(2)
	for _, sh := range s.shards {
		sh.mu.Lock()
		sh.removedSinceRebuild = compactRemovedThreshold + 1
		sh.mu.Unlock()
	}
	s.MaybeCompactAll()
	for i, sh := range s.shards {
		if sh.needsCompaction() {
			t.Fatalf("shard %d not compacted", i)
		}
	}
}

func TestCompactNowResetsWarmTier(t *testing.T) {
	idx := NewMemoryIndex()
	wt, err := NewWarmTier(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetWarmTier(wt)
	if err := idx.Index(testResource("c", "", "v1", "Pod", "ns", "p1")); err != nil {
		t.Fatal(err)
	}
	if err := wt.Store(CompactResource{ID: 42}); err != nil {
		t.Fatal(err)
	}
	idx.CompactNow()
	if wt.Count() != 0 {
		t.Fatalf("warm tier not reset on compaction: %d records", wt.Count())
	}
}

func TestSetupWarmTiersGivesEachShardItsOwn(t *testing.T) {
	s := NewShardedIndex(4)
	if err := s.SetupWarmTiers(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	seen := make(map[*WarmTier]bool)
	for i, sh := range s.shards {
		sh.mu.RLock()
		wt := sh.warmTier
		sh.mu.RUnlock()
		if wt == nil {
			t.Fatalf("shard %d has no warm tier", i)
		}
		if seen[wt] {
			t.Fatalf("shard %d shares a warm tier", i)
		}
		seen[wt] = true
	}
}

func TestWarmTiersDoNotCollideOnDocID(t *testing.T) {
	dir := t.TempDir()
	wt0, err := NewWarmTier(filepath.Join(dir, "s0"), nil)
	if err != nil {
		t.Fatal(err)
	}
	wt1, err := NewWarmTier(filepath.Join(dir, "s1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt0.Store(CompactResource{ID: 5, Name: 100}); err != nil {
		t.Fatal(err)
	}
	if err := wt1.Store(CompactResource{ID: 5, Name: 200}); err != nil {
		t.Fatal(err)
	}
	got0, ok0 := wt0.Get(5)
	got1, ok1 := wt1.Get(5)
	if !ok0 || !ok1 || got0.Name != 100 || got1.Name != 200 {
		t.Fatalf("warm tiers collided: %v %v %v %v", ok0, got0.Name, ok1, got1.Name)
	}
}

func TestAtCapacityIsPerShard(t *testing.T) {
	s := NewShardedIndex(2)
	var target *MemoryIndex
	for _, c := range []string{"aa", "ab", "ac", "ad"} {
		if s.shardIndex(c) == 0 {
			target = s.shards[0]
			for i := 0; i < 10; i++ {
				if err := s.Index(testResource(c, "", "v1", "Pod", "ns", fmt.Sprintf("p%d", i))); err != nil {
					t.Fatal(err)
				}
			}
			break
		}
	}
	if target == nil {
		t.Skip("no cluster hashed to shard 0")
	}
	if !s.atCapacity(10) {
		t.Fatal("one full shard must report capacity even if total < shards*threshold")
	}
	if s.atCapacity(11) {
		t.Fatal("under-threshold shards must not report capacity")
	}
}

func TestRemoveClusterDropsAllClusterDocs(t *testing.T) {
	s := NewShardedIndex(DefaultSearchShards)
	for i := 0; i < 20; i++ {
		if err := s.Index(testResource("gone", "", "v1", "Pod", "ns", fmt.Sprintf("g%d", i))); err != nil {
			t.Fatal(err)
		}
		if err := s.Index(testResource("kept", "", "v1", "Pod", "ns", fmt.Sprintf("k%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	removed := s.RemoveCluster("gone")
	if removed != 20 {
		t.Fatalf("expected 20 removed, got %d", removed)
	}
	if s.DocumentCount() != 20 {
		t.Fatalf("expected 20 docs left, got %d", s.DocumentCount())
	}
	res, _ := s.Search(SearchQuery{Text: "g1", Cluster: "gone", Limit: 10})
	if len(res) != 0 {
		t.Fatalf("removed cluster still searchable: %d results", len(res))
	}
}
