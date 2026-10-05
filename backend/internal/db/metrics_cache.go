package db

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const metricsLastUsedEvery = time.Hour

type MetricsQuery struct {
	Key  string `gorm:"primaryKey"`
	Data []byte
	// Bytes is indexed so the total is read from the index at open, not
	// from every page of a table of up to 128MB of blobs.
	Bytes    int64 `gorm:"index"`
	LastUsed int64 `gorm:"index"`
}

func (db *DB) OpenMetricsCache(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	g, err := gorm.Open(sqlite.Open("file:"+path+"?mode=rwc&_pragma=auto_vacuum(INCREMENTAL)&_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}
	sqlDB, err := g.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := g.AutoMigrate(&MetricsQuery{}); err != nil {
		_ = sqlDB.Close()
		return err
	}
	var total int64
	if err := g.Model(&MetricsQuery{}).Select("COALESCE(SUM(bytes), 0)").Scan(&total).Error; err != nil {
		_ = sqlDB.Close()
		return err
	}
	db.metricsCacheMu.Lock()
	db.metricsCache = g
	db.metricsCacheBytes = total
	db.metricsCacheMu.Unlock()
	return nil
}

func (db *DB) CloseMetricsCache() error {
	db.metricsCacheMu.Lock()
	defer db.metricsCacheMu.Unlock()
	if db.metricsCache == nil {
		return nil
	}
	sqlDB, err := db.metricsCache.DB()
	if err != nil {
		return err
	}
	db.metricsCache = nil
	return sqlDB.Close()
}

func (db *DB) GetMetricsQuery(key string) ([]byte, error) {
	db.metricsCacheMu.Lock()
	defer db.metricsCacheMu.Unlock()
	if db.metricsCache == nil {
		return nil, fmt.Errorf("metrics cache unavailable")
	}
	var row MetricsQuery
	if err := db.metricsCache.Where("key = ?", key).First(&row).Error; err != nil {
		return nil, err
	}
	// Eviction only needs a rough age, so a read refreshes it at most once per
	// metricsLastUsedEvery instead of turning every chart load into a write.
	if now := time.Now(); now.Sub(time.Unix(0, row.LastUsed)) >= metricsLastUsedEvery {
		if err := db.metricsCache.Model(&MetricsQuery{}).Where("key = ?", key).Update("last_used", now.UnixNano()).Error; err != nil {
			return nil, err
		}
	}
	return row.Data, nil
}

// Prune and insert in one transaction, so simultaneous queries cannot grow
// the cache past its budget. An oversized entry is served without retention.
// The budget is checked against a running total: summing the table on every
// save read all of it, ~70ms once the cache had filled, and charts save on
// most refreshes.
func (db *DB) SaveMetricsQuery(key string, data []byte, maxBytes int64) error {
	db.metricsCacheMu.Lock()
	defer db.metricsCacheMu.Unlock()
	if db.metricsCache == nil {
		return fmt.Errorf("metrics cache unavailable")
	}
	size := int64(len(key) + len(data) + 128)
	if size > maxBytes {
		return nil
	}
	removed := false
	var total int64
	err := db.metricsCache.Transaction(func(tx *gorm.DB) error {
		total = db.metricsCacheBytes
		var replaced []int64
		if err := tx.Model(&MetricsQuery{}).Where("key = ?", key).Pluck("bytes", &replaced).Error; err != nil {
			return err
		}
		if len(replaced) > 0 {
			if err := tx.Where("key = ?", key).Delete(&MetricsQuery{}).Error; err != nil {
				return err
			}
			total -= replaced[0]
		}
		for total+size > maxBytes {
			var rows []MetricsQuery
			if err := tx.Select("key, bytes").Order("last_used ASC, key ASC").Limit(64).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
				total = 0 // nothing left to evict, so nothing left to count
				break
			}
			for _, row := range rows {
				if total+size <= maxBytes {
					break
				}
				if err := tx.Where("key = ?", row.Key).Delete(&MetricsQuery{}).Error; err != nil {
					return err
				}
				total -= row.Bytes
				removed = true
			}
		}
		if err := tx.Create(&MetricsQuery{Key: key, Data: data, Bytes: size, LastUsed: time.Now().UnixNano()}).Error; err != nil {
			return err
		}
		total += size
		return nil
	})
	if err == nil {
		db.metricsCacheBytes = total
	}
	if err == nil && removed {
		if rows, e := db.metricsCache.Raw("PRAGMA incremental_vacuum").Rows(); e == nil {
			for rows.Next() {
			}
			rows.Close()
		}
	}
	return err
}

func (db *DB) MetricsCacheAvailable() bool { return db.metricsCache != nil }
