package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openMainDSN(t *testing.T) *DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(mainDSN(filepath.Join(t.TempDir(), "kanivet.db"))), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(10)
	t.Cleanup(func() { _ = sqlDB.Close() })
	d := &DB{DB: g}
	if err := d.MigrateEvents(); err != nil {
		t.Fatalf("migrate events: %v", err)
	}
	return d
}

func pooledConns(t *testing.T, d *DB, n int) []*sql.Conn {
	t.Helper()
	sqlDB, err := d.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	conns := make([]*sql.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, err := sqlDB.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		conns = append(conns, c)
	}
	return conns
}

// Pragmas set with Exec after opening reached only the one pooled connection
// that ran it; the rest kept SQLite's defaults.
func TestMainDSNConfiguresEveryPooledConnection(t *testing.T) {
	d := openMainDSN(t)
	for i, c := range pooledConns(t, d, 4) {
		var cacheSize, mmapSize, tempStore int64
		var journal string
		ctx := context.Background()
		if err := c.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cacheSize); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA mmap_size").Scan(&mmapSize); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&tempStore); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if cacheSize != -16000 || mmapSize != 268435456 || tempStore != 2 || journal != "wal" {
			t.Errorf("connection %d: cache_size=%d mmap_size=%d temp_store=%d journal_mode=%s", i, cacheSize, mmapSize, tempStore, journal)
		}
	}
}

// In shared-cache mode a write transaction locks the table for every other
// connection of the process, so the event listener's batches stalled the
// events and incidents views. With a private cache per connection WAL lets
// readers see the last committed state while a write is open.
func TestMainDSNReadsProceedDuringAnOpenWrite(t *testing.T) {
	d := openMainDSN(t)
	if err := d.StoreEvents("c1", []K8sEvent{{UID: "committed", Name: "e", EventTime: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	conns := pooledConns(t, d, 2)
	writer, reader := conns[0], conns[1]
	ctx := context.Background()
	if _, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, "INSERT INTO k8s_events (cluster, uid, name) VALUES ('c1', 'pending', 'e')"); err != nil {
		t.Fatal(err)
	}
	defer writer.ExecContext(ctx, "ROLLBACK")

	// A blocked read does not honor its context, so wait for it here.
	var n int
	done := make(chan error, 1)
	go func() {
		done <- reader.QueryRowContext(ctx, "SELECT count(*) FROM k8s_events WHERE cluster = 'c1'").Scan(&n)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read during an open write failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read blocked behind an open write transaction")
	}
	if n != 1 {
		t.Fatalf("read saw %d rows, want only the committed one", n)
	}
}

// Each cluster's event listener stores its batches on its own. A deferred
// transaction reads before it writes, and once another connection has written
// since that read SQLite refuses the upgrade at once instead of waiting out
// busy_timeout, so the batch was dropped. Transactions now take the write lock
// when they begin and queue behind one another.
func TestMainDSNQueuesConcurrentEventWriters(t *testing.T) {
	d := openMainDSN(t)
	const writers, batches = 4, 20
	errs := make(chan error, writers*batches)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(cluster string) {
			defer wg.Done()
			for i := 0; i < batches; i++ {
				// Half of each batch updates events stored by earlier batches.
				batch := make([]K8sEvent, 0, 20)
				for j := 0; j < 20; j++ {
					uid := fmt.Sprintf("%s-%d", cluster, j)
					if j%2 == 0 {
						uid = fmt.Sprintf("%s-%d-%d", cluster, i, j)
					}
					batch = append(batch, K8sEvent{UID: uid, Name: uid, EventTime: time.Now()})
				}
				if err := d.StoreEvents(cluster, batch); err != nil {
					errs <- err
				}
			}
		}(fmt.Sprintf("c%d", w))
	}
	wg.Wait()
	close(errs)
	failed := 0
	var first error
	for err := range errs {
		if first == nil {
			first = err
		}
		failed++
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent event batches failed, first: %v", failed, writers*batches, first)
	}
}

func queryPlan(t *testing.T, d *DB, query string, args ...interface{}) string {
	t.Helper()
	var rows []struct {
		ID      int
		Parent  int
		Notused int
		Detail  string
	}
	if err := d.Raw("EXPLAIN QUERY PLAN "+query, args...).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	details := make([]string, 0, len(rows))
	for _, r := range rows {
		details = append(details, r.Detail)
	}
	return strings.Join(details, " | ")
}

func TestClusterEventQueriesAvoidSorting(t *testing.T) {
	d := openMainDSN(t)
	for c := 0; c < 2; c++ {
		rows := make([]K8sEvent, 0, 500)
		for i := 0; i < 500; i++ {
			ts := time.Now().Add(-time.Duration(i) * time.Second)
			rows = append(rows, K8sEvent{Cluster: fmt.Sprintf("c%d", c), UID: fmt.Sprintf("%d-%d", c, i), LastTimestamp: ts, EventTime: ts})
		}
		if err := d.CreateInBatches(rows, 250).Error; err != nil {
			t.Fatal(err)
		}
	}
	for name, query := range map[string]string{
		"GetClusterEvents":       "SELECT * FROM k8s_events WHERE cluster = ? ORDER BY last_timestamp DESC, event_time DESC LIMIT 1000",
		"GetRecentClusterEvents": "SELECT * FROM k8s_events WHERE cluster = ? ORDER BY event_time DESC LIMIT 20",
	} {
		if plan := queryPlan(t, d, query, "c1"); strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("%s sorts the cluster's rows: %s", name, plan)
		}
	}
}

// Events recorded through events.k8s.io carry only eventTime; their
// lastTimestamp is empty. They must rank by when they happened.
func TestGetRecentClusterEventsRanksByEventTime(t *testing.T) {
	d := newTestDB(t)
	now := time.Now()
	rows := []K8sEvent{
		{Cluster: "c1", UID: "legacy-old", Reason: "BackOff", LastTimestamp: now.Add(-10 * time.Minute), EventTime: now.Add(-10 * time.Minute)},
		{Cluster: "c1", UID: "scheduled-new", Reason: "Scheduled", EventTime: now.Add(-time.Minute)},
		{Cluster: "c1", UID: "legacy-mid", Reason: "Pulled", LastTimestamp: now.Add(-5 * time.Minute), EventTime: now.Add(-5 * time.Minute)},
		{Cluster: "c2", UID: "other-cluster", Reason: "Other", EventTime: now},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := d.GetRecentClusterEvents("c1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UID != "scheduled-new" || got[1].UID != "legacy-mid" {
		uids := make([]string, len(got))
		for i, e := range got {
			uids[i] = e.UID
		}
		t.Fatalf("got %v, want [scheduled-new legacy-mid]", uids)
	}
}
