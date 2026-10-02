package db

import (
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// RightsizingReport is the last computed rightsizing report for a cluster,
// profile and window, so reopening the view shows it at once while a fresh
// one computes. It is a cache: losing it costs one recomputation.
type RightsizingReport struct {
	Key       string `gorm:"primaryKey"`
	Cluster   string `gorm:"index"`
	Data      []byte
	UpdatedAt int64
}

// RightsizingDismissal hides a recommendation the user decided against. An
// empty Container dismisses the whole workload; a zero Until is permanent.
type RightsizingDismissal struct {
	ID        uint   `gorm:"primaryKey"`
	Cluster   string `gorm:"index:idx_rs_dismissal,priority:1"`
	Namespace string `gorm:"index:idx_rs_dismissal,priority:2"`
	Kind      string `gorm:"index:idx_rs_dismissal,priority:3"`
	Name      string `gorm:"index:idx_rs_dismissal,priority:4"`
	// VClusterNamespace tells apart same-named workloads synced from
	// different virtual namespaces into one host namespace.
	VClusterNamespace string `gorm:"column:vcluster_namespace"`
	Container         string
	Reason            string
	Until             int64
	CreatedAt         int64
}

// RightsizingChunk is one finished UTC day of one history query, compressed.
// Past days never change, so a refresh only fetches the day in progress.
type RightsizingChunk struct {
	Key      string `gorm:"primaryKey"`
	Cluster  string `gorm:"index"`
	Day      int64  `gorm:"index"`
	Data     []byte
	Bytes    int
	LastUsed int64 `gorm:"index"`
}

const (
	// chunkMaxAge keeps the longest window (28 days) plus slack.
	chunkMaxAge = 30 * 24 * time.Hour
	// chunkMaxBytes caps the history cache on disk. Days are stored at about
	// 1-2 bytes a sample, so this holds several weeks of a few large clusters.
	chunkMaxBytes = 128 << 20
	// lastUsedEvery limits how often a cache hit writes its access time.
	lastUsedEvery = 12 * time.Hour
)

// OpenRightsizingCache keeps history chunks and reports in a file of their
// own. Everything in it can be recomputed, so it can be deleted at any time;
// incremental auto-vacuum hands pruned space back to the disk, which the main
// database never does.
func (db *DB) OpenRightsizingCache(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// auto_vacuum only takes effect when set before the first table exists;
	// journal_size_limit stops the WAL file from keeping its high-water size.
	dsn := "file:" + path + "?mode=rwc&_pragma=auto_vacuum(INCREMENTAL)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=journal_size_limit(4194304)"
	g, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}
	if err := g.AutoMigrate(&RightsizingReport{}, &RightsizingChunk{}); err != nil {
		return err
	}
	db.rsCache = g
	return nil
}

// CloseRightsizingCache closes the cache file. Windows can't delete or
// replace a file that is still open.
func (db *DB) CloseRightsizingCache() error {
	if db.rsCache == nil {
		return nil
	}
	sqlDB, err := db.rsCache.DB()
	if err != nil {
		return err
	}
	db.rsCache = nil
	return sqlDB.Close()
}

func (db *DB) cache() *gorm.DB {
	if db.rsCache != nil {
		return db.rsCache
	}
	return db.DB
}

func (db *DB) MigrateRightsizing() error {
	if err := db.AutoMigrate(&RightsizingDismissal{}); err != nil {
		return err
	}
	if db.rsCache == nil {
		if err := db.AutoMigrate(&RightsizingReport{}, &RightsizingChunk{}); err != nil {
			return err
		}
	}
	db.PruneRightsizingChunks()
	cutoff := time.Now().AddDate(0, 0, -30).Unix()
	db.cache().Where("updated_at < ?", cutoff).Delete(&RightsizingReport{})
	db.DB.Where("until > 0 AND until < ?", time.Now().Unix()).Delete(&RightsizingDismissal{})
	return nil
}

func (db *DB) SaveRightsizingReport(key, cluster string, data []byte) error {
	r := RightsizingReport{Key: key, Cluster: cluster, Data: data, UpdatedAt: time.Now().Unix()}
	return db.cache().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"cluster", "data", "updated_at"}),
	}).Create(&r).Error
}

func (db *DB) GetRightsizingReport(key string) ([]byte, error) {
	var r RightsizingReport
	if err := db.cache().Select("data").Where("key = ?", key).First(&r).Error; err != nil {
		return nil, err
	}
	return r.Data, nil
}

func (db *DB) ListRightsizingDismissals(cluster string) ([]RightsizingDismissal, error) {
	var out []RightsizingDismissal
	err := db.DB.Where("cluster = ? AND (until = 0 OR until > ?)", cluster, time.Now().Unix()).Find(&out).Error
	return out, err
}

func (db *DB) SaveRightsizingDismissal(d *RightsizingDismissal) error {
	db.DB.Where("cluster = ? AND namespace = ? AND vcluster_namespace = ? AND kind = ? AND name = ? AND container = ?",
		d.Cluster, d.Namespace, d.VClusterNamespace, d.Kind, d.Name, d.Container).Delete(&RightsizingDismissal{})
	d.ID = 0
	d.CreatedAt = time.Now().Unix()
	return db.DB.Create(d).Error
}

func (db *DB) DeleteRightsizingDismissal(cluster, namespace, vclusterNamespace, kind, name, container string) error {
	return db.DB.Where("cluster = ? AND namespace = ? AND vcluster_namespace = ? AND kind = ? AND name = ? AND container = ?",
		cluster, namespace, vclusterNamespace, kind, name, container).Delete(&RightsizingDismissal{}).Error
}

func (db *DB) GetRightsizingChunk(key string) ([]byte, error) {
	var c RightsizingChunk
	if err := db.cache().Select("data, last_used").Where("key = ?", key).First(&c).Error; err != nil {
		return nil, err
	}
	// Eviction only needs coarse recency; a write on every hit would turn
	// each report into hundreds of disk writes.
	if now := time.Now(); now.Sub(time.Unix(c.LastUsed, 0)) > lastUsedEvery {
		db.cache().Model(&RightsizingChunk{}).Where("key = ?", key).Update("last_used", now.Unix())
	}
	return c.Data, nil
}

func (db *DB) SaveRightsizingChunk(key, cluster string, day int64, data []byte) error {
	c := RightsizingChunk{Key: key, Cluster: cluster, Day: day, Data: data, Bytes: len(data), LastUsed: time.Now().Unix()}
	return db.cache().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "bytes", "last_used"}),
	}).Create(&c).Error
}

// PruneRightsizingChunks drops days too old for any window, then the least
// recently used chunks until the cache fits its size budget.
// Freed pages go back to the file system.
func (db *DB) PruneRightsizingChunks() {
	c := db.cache()
	removed := c.Where("day < ?", time.Now().Add(-chunkMaxAge).Unix()).Delete(&RightsizingChunk{}).RowsAffected
	var total int64
	c.Model(&RightsizingChunk{}).Select("COALESCE(SUM(bytes), 0)").Scan(&total)
	for total > chunkMaxBytes {
		var oldest []RightsizingChunk
		if c.Select("key, bytes").Order("last_used").Limit(200).Find(&oldest).Error != nil || len(oldest) == 0 {
			break
		}
		keys := make([]string, len(oldest))
		for i, o := range oldest {
			keys[i] = o.Key
			total -= int64(o.Bytes)
		}
		removed += c.Where("key IN ?", keys).Delete(&RightsizingChunk{}).RowsAffected
	}
	if removed > 0 && db.rsCache != nil {
		// The pragma frees one page per step: read it to the end.
		if rows, err := db.rsCache.Raw("PRAGMA incremental_vacuum").Rows(); err == nil {
			for rows.Next() {
			}
			rows.Close()
		}
		db.rsCache.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
}

// ForgetRightsizingClusters deletes the reports and history days stored for
// every cluster that match accepts. They were read from a metrics source whose
// settings just changed (another Mimir tenant or instance), so serving them
// would show one tenant's data under another's name until they expire.
func (db *DB) ForgetRightsizingClusters(match func(cluster string) bool) error {
	c := db.cache()
	var clusters []string
	for _, model := range []any{&RightsizingReport{}, &RightsizingChunk{}} {
		var found []string
		if err := c.Model(model).Distinct("cluster").Pluck("cluster", &found).Error; err != nil {
			return err
		}
		for _, cl := range found {
			if match(cl) && !slices.Contains(clusters, cl) {
				clusters = append(clusters, cl)
			}
		}
	}
	if len(clusters) == 0 {
		return nil
	}
	if err := c.Where("cluster IN ?", clusters).Delete(&RightsizingReport{}).Error; err != nil {
		return err
	}
	return c.Where("cluster IN ?", clusters).Delete(&RightsizingChunk{}).Error
}
