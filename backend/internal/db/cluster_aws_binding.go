package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClusterAWSBinding records which IAM Identity Center account and role Kanivet
// uses for a kubeconfig context whose exec block names no profile. It lives
// here, not in the kubeconfig, so kubectl in a terminal keeps its own behaviour.
type ClusterAWSBinding struct {
	Cluster   string `gorm:"primaryKey" json:"cluster"`
	StartURL  string `gorm:"not null" json:"startUrl"`
	AccountID string `gorm:"not null" json:"accountId"`
	RoleName  string `gorm:"not null" json:"roleName"`
	Profile   string `gorm:"not null" json:"profile"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (db *DB) GetClusterAWSBindings() ([]ClusterAWSBinding, error) {
	var bindings []ClusterAWSBinding
	if err := db.DB.Find(&bindings).Error; err != nil {
		return nil, err
	}
	return bindings, nil
}

// GetClusterAWSBinding returns nil, nil for a context with no binding.
func (db *DB) GetClusterAWSBinding(cluster string) (*ClusterAWSBinding, error) {
	var binding ClusterAWSBinding
	err := db.DB.Where("cluster = ?", cluster).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (db *DB) SaveClusterAWSBinding(binding *ClusterAWSBinding) error {
	binding.UpdatedAt = time.Now().UnixMilli()
	return db.DB.Clauses(clause.OnConflict{UpdateAll: true}).Create(binding).Error
}

func (db *DB) DeleteClusterAWSBinding(cluster string) error {
	return db.DB.Where("cluster = ?", cluster).Delete(&ClusterAWSBinding{}).Error
}
