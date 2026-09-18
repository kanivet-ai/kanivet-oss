package db

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"
)

// absentClusterGrace is how long a cluster must be missing from every
// kubeconfig before its cached rows are dropped. It protects against a
// kubeconfig that is temporarily unreadable or unmounted: the rows only go
// once nothing has refreshed them for this long.
const absentClusterGrace = 7 * 24 * time.Hour

// fullVacuumMinFree is the smallest amount of free space inside the database
// file that justifies a full VACUUM outside the one-time migration.
const fullVacuumMinFree = 64 << 20

// StartMaintenance runs retention in the background: once shortly after start
// and then daily. keep returns the clusters currently present in kubeconfigs;
// rows for clusters absent from it long enough are dropped and onPurged is
// told which ones, then stale rows are purged and freed pages are returned to
// the filesystem.
func (db *DB) StartMaintenance(ctx context.Context, keep func() ([]string, error), onPurged func(clusters []string)) {
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			db.runMaintenance(keep, onPurged)
			timer.Reset(24 * time.Hour)
		}
	}()
}

func (db *DB) runMaintenance(keep func() ([]string, error), onPurged func(clusters []string)) {
	start := time.Now()
	var removed int64
	if keep != nil {
		clusters, err := keep()
		switch {
		case err != nil:
			log.Printf("[DB] maintenance: cluster list unavailable, skipping cluster purge: %v", err)
		default:
			purged, n, err := db.PurgeClustersNotIn(clusters, time.Now())
			if err != nil {
				log.Printf("[DB] maintenance: cluster purge failed: %v", err)
			}
			removed += n
			if len(purged) > 0 && onPurged != nil {
				onPurged(purged)
			}
		}
	}
	n, err := db.PurgeStale(time.Now())
	if err != nil {
		log.Printf("[DB] maintenance: stale purge failed: %v", err)
	}
	removed += n
	db.reclaimSpace(false)
	if removed > 0 {
		log.Printf("[DB] maintenance removed %d rows in %v", removed, time.Since(start).Round(time.Millisecond))
	}
}

// PurgeStale deletes search rows not refreshed within searchRowRetention and
// stored events older than eventRetention. It returns the rows removed.
func (db *DB) PurgeStale(now time.Time) (int64, error) {
	var total int64
	res := db.Where("indexed_at < ?", now.Add(-searchRowRetention)).Delete(&SearchableResource{})
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected
	res = db.Where("created_at < ?", now.Add(-eventRetention)).Delete(&K8sEvent{})
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected
	return total, nil
}

// perClusterTables lists every table holding per-cluster cached state along
// with the column that records when a row was last written. Snapshots store
// theirs as a unix timestamp; the others as driver-formatted text.
var perClusterTables = []struct {
	table, freshness string
	unix             bool
}{
	{"searchable_resources", "indexed_at", false},
	{"k8s_events", "created_at", false},
	{"event_listeners", "updated_at", false},
	{"list_snapshots", "updated_at", true},
}

// PurgeClustersNotIn removes cached rows for clusters absent from keep whose
// newest write in any per-cluster table is older than absentClusterGrace. A
// vcluster is kept while its host cluster is kept. An empty keep list purges
// nothing: it almost always means the kubeconfigs could not be read. It
// returns the purged clusters and the number of rows removed.
func (db *DB) PurgeClustersNotIn(keep []string, now time.Time) ([]string, int64, error) {
	if len(keep) == 0 {
		return nil, 0, nil
	}
	keepSet := make(map[string]struct{}, len(keep))
	for _, c := range keep {
		keepSet[c] = struct{}{}
	}
	kept := func(cluster string) bool {
		if _, ok := keepSet[cluster]; ok {
			return true
		}
		if host, ok := vclusterHost(cluster); ok {
			_, ok = keepSet[host]
			return ok
		}
		return false
	}

	// Newest write per cluster across every table. A timestamp that fails to
	// parse counts as fresh, which errs on the side of keeping data.
	newest := make(map[string]time.Time)
	seen := make(map[string]struct{})
	for _, t := range perClusterTables {
		var rows []struct {
			Cluster string
			Newest  string
		}
		q := "SELECT cluster, MAX(" + t.freshness + ") AS newest FROM " + t.table + " GROUP BY cluster"
		if err := db.Raw(q).Scan(&rows).Error; err != nil {
			return nil, 0, err
		}
		for _, r := range rows {
			seen[r.Cluster] = struct{}{}
			var ts time.Time
			if t.unix {
				secs, err := strconv.ParseInt(r.Newest, 10, 64)
				if err != nil {
					ts = now
				} else {
					ts = time.Unix(secs, 0)
				}
			} else {
				parsed, ok := parseSQLiteTime(r.Newest)
				ts = parsed
				if !ok {
					ts = now
				}
			}
			if ts.After(newest[r.Cluster]) {
				newest[r.Cluster] = ts
			}
		}
	}

	cutoff := now.Add(-absentClusterGrace)
	var purged []string
	var removed int64
	for cluster := range seen {
		if kept(cluster) || newest[cluster].After(cutoff) {
			continue
		}
		for _, t := range perClusterTables {
			res := db.Exec("DELETE FROM "+t.table+" WHERE cluster = ?", cluster)
			if res.Error != nil {
				return purged, removed, res.Error
			}
			removed += res.RowsAffected
		}
		purged = append(purged, cluster)
	}
	sortStrings(purged)
	return purged, removed, nil
}

// sqliteTimeLayouts are the text forms the sqlite driver has written
// time.Time values in across releases.
var sqliteTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

func parseSQLiteTime(s string) (time.Time, bool) {
	for _, layout := range sqliteTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// vclusterHost extracts the host cluster from a vcluster ID of the form
// "vcluster:<host>:<namespace>:<name>". It mirrors the k8s package's parser
// without importing client-go into the database layer.
func vclusterHost(id string) (string, bool) {
	const prefix = "vcluster:"
	if !strings.HasPrefix(id, prefix) {
		return "", false
	}
	rest := id[len(prefix):]
	iName := strings.LastIndex(rest, ":")
	if iName < 0 {
		return "", false
	}
	iNs := strings.LastIndex(rest[:iName], ":")
	if iNs < 0 {
		return "", false
	}
	return rest[:iNs], true
}

// reclaimSpace hands freed pages back to the filesystem. When force is set,
// or the free space inside the file is worth a rewrite, it runs a full VACUUM
// provided the volume has room for the temporary copy; otherwise it falls
// back to incremental_vacuum, which is a no-op until a full VACUUM has enabled
// auto_vacuum on this file.
func (db *DB) reclaimSpace(force bool) {
	var pageSize, pageCount, freelist int64
	db.Raw("PRAGMA page_size").Scan(&pageSize)
	db.Raw("PRAGMA page_count").Scan(&pageCount)
	db.Raw("PRAGMA freelist_count").Scan(&freelist)
	fileSize := pageSize * pageCount
	free := pageSize * freelist
	wantFull := force || free >= fullVacuumMinFree || (fileSize > 0 && free >= fileSize/5)
	if !wantFull {
		if freelist > 0 {
			db.Exec("PRAGMA incremental_vacuum")
		}
		return
	}
	if path := db.filePath(); path != "" {
		avail, err := freeDiskBytes(path)
		needed := uint64(fileSize + fileSize/5 + fullVacuumMinFree)
		if err == nil && avail < needed {
			log.Printf("[DB] skipping VACUUM: %d MB free on volume, need %d MB (database %d MB, %d MB reclaimable)",
				avail>>20, needed>>20, fileSize>>20, free>>20)
			return
		}
	}
	start := time.Now()
	if err := db.Exec("VACUUM").Error; err != nil {
		log.Printf("[DB] VACUUM failed: %v", err)
		return
	}
	var after int64
	db.Raw("PRAGMA page_count").Scan(&after)
	log.Printf("[DB] VACUUM reclaimed %d MB in %v (database now %d MB)", (pageCount-after)*pageSize>>20, time.Since(start).Round(time.Millisecond), after*pageSize>>20)
}

// filePath returns the main database file, or "" for in-memory databases.
func (db *DB) filePath() string {
	var rows []struct {
		Name string
		File string
	}
	if err := db.Raw("PRAGMA database_list").Scan(&rows).Error; err != nil {
		return ""
	}
	for _, r := range rows {
		if r.Name == "main" {
			return r.File
		}
	}
	return ""
}
