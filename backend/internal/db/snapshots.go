package db

import (
	"time"

	"gorm.io/gorm/clause"
)

type ListSnapshot struct {
	Topic     string `gorm:"primaryKey"`
	Cluster   string `gorm:"index"`
	Items     []byte
	UpdatedAt int64
}

func (db *DB) MigrateSnapshots() error {
	if err := db.AutoMigrate(&ListSnapshot{}); err != nil {
		return err
	}
	cutoff := time.Now().AddDate(0, 0, -30).Unix()
	db.DB.Where("updated_at < ?", cutoff).Delete(&ListSnapshot{})
	return nil
}

func (db *DB) SaveListSnapshot(topic, cluster string, items []byte) error {
	snap := ListSnapshot{Topic: topic, Cluster: cluster, Items: items, UpdatedAt: time.Now().Unix()}
	return db.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "topic"}},
		DoUpdates: clause.AssignmentColumns([]string{"cluster", "items", "updated_at"}),
	}).Create(&snap).Error
}

func (db *DB) GetListSnapshot(topic string) ([]byte, error) {
	var snap ListSnapshot
	if err := db.DB.Select("items").Where("topic = ?", topic).First(&snap).Error; err != nil {
		return nil, err
	}
	return snap.Items, nil
}
