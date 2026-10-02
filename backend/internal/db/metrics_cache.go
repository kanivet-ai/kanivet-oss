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

type MetricsQuery struct {
	Key      string `gorm:"primaryKey"`
	Data     []byte
	Bytes    int64
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
	db.metricsCache = g
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
	if err := db.metricsCache.Model(&MetricsQuery{}).Where("key = ?", key).Update("last_used", time.Now().UnixNano()).Error; err != nil {
		return nil, err
	}
	return row.Data, nil
}

// Prune and insert in one transaction, so simultaneous queries cannot grow
// the cache past its budget. An oversized entry is served without retention.
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
	err := db.metricsCache.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("key = ?", key).Delete(&MetricsQuery{}).Error; err != nil {
			return err
		}
		var total int64
		if err := tx.Model(&MetricsQuery{}).Select("COALESCE(SUM(bytes), 0)").Scan(&total).Error; err != nil {
			return err
		}
		for total+size > maxBytes {
			var rows []MetricsQuery
			if err := tx.Select("key, bytes").Order("last_used ASC, key ASC").Limit(64).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
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
		return tx.Create(&MetricsQuery{Key: key, Data: data, Bytes: size, LastUsed: time.Now().UnixNano()}).Error
	})
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
