package search

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/search/storage"
)

func newDBBackedService(t *testing.T) *Service {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database := &db.DB{DB: gdb}
	if err := database.MigrateSearch(); err != nil {
		t.Fatalf("migrate search: %v", err)
	}
	s := newTestSearchService()
	s.db = database
	s.index = storage.NewShardedIndex(3)
	s.indexedResources = make(map[string]map[string]time.Time)
	s.loadedClusters = make(map[string]bool)
	return s
}

func seedCluster(t *testing.T, s *Service, cluster string, docs int, indexedAt time.Time) {
	t.Helper()
	rows := []db.SearchableResource{{
		ResourceID: "kind:" + cluster + ":apps:v1:Deployment", Cluster: cluster, Kind: "KindDefinition", Name: "Deployment",
		Category: "Kind", APIVersion: "v1", ResourceGroup: "apps", ResourceVersion: "v1", Keywords: "Deployment deployment deployments", IndexedAt: indexedAt,
	}}
	for i := 0; i < docs; i++ {
		rows = append(rows, db.SearchableResource{
			ResourceID: fmt.Sprintf("%s/apps/v1/deployments/ns/api-%d", cluster, i), Cluster: cluster, Kind: "Deployment",
			APIVersion: "apps/v1", Name: fmt.Sprintf("api-%d", i), Namespace: "ns", Category: "Workloads",
			ResourceGroup: "apps", ResourceVersion: "v1", IndexedAt: indexedAt, UpdatedAt: indexedAt,
		})
	}
	if err := s.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed %s: %v", cluster, err)
	}
}

func clusterHits(t *testing.T, s *Service, cluster string) int {
	t.Helper()
	res, err := s.index.Search(storage.SearchQuery{Text: "api", Clusters: []string{cluster}, Limit: 100})
	if err != nil {
		t.Fatalf("search %s: %v", cluster, err)
	}
	return len(res)
}

// Boot loads the most recently indexed clusters until the budget is spent,
// keeps every kind definition, flags that the database still holds more, and
// pulls a skipped cluster in when its tab opens.
func TestPersistedLoadHonoursBudgetMostRecentFirst(t *testing.T) {
	s := newDBBackedService(t)
	now := time.Now()
	seedCluster(t, s, "old", 4, now.Add(-72*time.Hour))
	seedCluster(t, s, "recent", 4, now.Add(-time.Hour))
	seedCluster(t, s, "newest", 4, now)

	prev := bootLoadBudget
	bootLoadBudget = 6
	defer func() { bootLoadBudget = prev }()

	s.loadPersistedIndexAsync()

	if got := clusterHits(t, s, "newest"); got != 4 {
		t.Fatalf("newest cluster docs in memory = %d, want 4", got)
	}
	if got := clusterHits(t, s, "recent"); got != 4 {
		t.Fatalf("recent cluster docs in memory = %d, want 4 (budget of 6 admits a second cluster)", got)
	}
	if got := clusterHits(t, s, "old"); got != 0 {
		t.Fatalf("old cluster must stay in the database, found %d docs in memory", got)
	}
	if !s.dbHasUnloaded.Load() {
		t.Fatal("service must record that the database holds unloaded clusters")
	}
	if !s.index.HasDocument("kind:old:apps:v1:Deployment") {
		t.Fatal("kind definitions must load for every cluster, including unloaded ones")
	}
	if got := s.index.DocumentCount(); got != 8+3 {
		t.Fatalf("document count = %d, want 8 docs + 3 kind definitions", got)
	}

	s.ensureClusterLoaded("old")
	if got := clusterHits(t, s, "old"); got != 4 {
		t.Fatalf("old cluster docs after open = %d, want 4", got)
	}
	s.ensureClusterLoaded("old")
	if got := clusterHits(t, s, "old"); got != 4 {
		t.Fatalf("second open must not duplicate, got %d", got)
	}
}

func TestPersistedLoadWithinBudgetLoadsEverything(t *testing.T) {
	s := newDBBackedService(t)
	seedCluster(t, s, "a", 3, time.Now())
	seedCluster(t, s, "b", 3, time.Now())
	s.loadPersistedIndexAsync()
	if s.dbHasUnloaded.Load() {
		t.Fatal("nothing was skipped, so the DB fallback flag must stay off")
	}
	if got := s.index.DocumentCount(); got != 6+2 {
		t.Fatalf("document count = %d, want 8", got)
	}
}
