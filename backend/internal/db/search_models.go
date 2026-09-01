package db

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// SearchableResource represents a Kubernetes resource in the search index
type SearchableResource struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	ResourceID string `gorm:"uniqueIndex;not null" json:"resource_id"` // cluster/group/version/kind/namespace/name
	Cluster    string `gorm:"index:idx_sr_cluster;index:idx_sr_cluster_kind;index:idx_sr_cluster_ns;uniqueIndex:idx_sr_natural_key;not null" json:"cluster"`
	Kind       string `gorm:"index:idx_sr_kind;index:idx_sr_cluster_kind;uniqueIndex:idx_sr_natural_key;not null" json:"kind"`
	APIVersion string `json:"api_version"`
	Name       string `gorm:"index:idx_sr_name;uniqueIndex:idx_sr_natural_key;not null" json:"name"`
	Namespace  string `gorm:"index:idx_sr_cluster_ns;uniqueIndex:idx_sr_natural_key" json:"namespace"`

	// Metadata
	Labels      string `json:"labels"`      // JSON string
	Annotations string `json:"annotations"` // JSON string

	// Search fields
	Description string `json:"description"`
	Keywords    string `json:"keywords"` // Space-separated keywords
	Category    string `gorm:"index" json:"category"`

	// Resource details - ResourceGroup is part of unique key to differentiate CRDs with same Kind
	ResourceGroup   string `gorm:"uniqueIndex:idx_sr_natural_key" json:"resource_group"`
	ResourceVersion string `json:"resource_version"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	IndexedAt time.Time `gorm:"index" json:"indexed_at"`

	// For FTS
	SearchText string `gorm:"-" json:"-"` // Virtual field for FTS
}

// SearchIndex represents the FTS5 virtual table
type SearchIndex struct {
	ResourceID string `gorm:"primaryKey"`
	Content    string // Combined searchable content
}

// IndexingStatus tracks the progress of indexing for each cluster
type IndexingStatus struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	Cluster          string     `gorm:"uniqueIndex;not null" json:"cluster"`
	IsRunning        bool       `json:"is_running"`
	Progress         float64    `json:"progress"`
	TotalResources   int        `json:"total_resources"`
	IndexedResources int        `json:"indexed_resources"`
	CurrentResource  string     `json:"current_resource"`
	StartedAt        *time.Time `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	LastError        string     `json:"last_error"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// SearchHistory stores recently clicked resources
type SearchHistory struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Name       string    `gorm:"not null" json:"name"`
	Kind       string    `gorm:"not null" json:"kind"`
	Namespace  string    `json:"namespace"`
	Cluster    string    `gorm:"not null" json:"cluster"`
	APIVersion string    `json:"api_version"`
	Category   string    `json:"category"`
	CreatedAt  time.Time `json:"created_at"`
}

// Migration functions

func (db *DB) MigrateSearch() error {
	var indexSQL string
	db.Raw("SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_sr_natural_key'").Scan(&indexSQL)
	if indexSQL != "" && !strings.Contains(indexSQL, "resource_group") {
		db.Exec("DROP INDEX IF EXISTS idx_sr_natural_key")
	}
	db.Exec(`DELETE FROM searchable_resources WHERE rowid NOT IN (
		SELECT MIN(rowid) FROM searchable_resources
		GROUP BY cluster, kind, name, namespace, resource_group)`)
	return db.AutoMigrate(&SearchableResource{}, &IndexingStatus{}, &SearchHistory{})
}

// Search methods

func (db *DB) SearchResources(query string, clusters []string, limit int, offset int) ([]SearchableResource, int64, error) {
	var resources []SearchableResource
	var total int64

	// Build base query
	baseQuery := db.Model(&SearchableResource{})
	if len(clusters) > 0 {
		baseQuery = baseQuery.Where("cluster IN ?", clusters)
	}

	// If query is empty, return all resources
	if query == "" {
		baseQuery.Count(&total)
		err := baseQuery.
			Order("kind, name").
			Limit(limit).
			Offset(offset).
			Find(&resources).Error
		return resources, total, err
	}

	// Use regular SQL search with LIKE
	searchQuery := "%" + strings.ToLower(query) + "%"

	// Build the WHERE clause for searching
	whereClause := db.Where(
		"LOWER(name) LIKE ? OR LOWER(kind) LIKE ? OR LOWER(namespace) LIKE ? OR LOWER(description) LIKE ? OR LOWER(keywords) LIKE ?",
		searchQuery, searchQuery, searchQuery, searchQuery, searchQuery,
	)

	// Add clusters filter if specified
	if len(clusters) > 0 {
		whereClause = whereClause.Where("cluster IN ?", clusters)
	}

	// Get total count
	if err := whereClause.Model(&SearchableResource{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	err := whereClause.
		Order("kind, name").
		Limit(limit).
		Offset(offset).
		Find(&resources).Error

	if err != nil {
		return nil, 0, err
	}

	return resources, total, nil
}

// Index management

func (db *DB) UpsertSearchableResource(resource *SearchableResource) error {
	resource.IndexedAt = time.Now()

	return db.Transaction(func(tx *gorm.DB) error {
		// Use ON CONFLICT for upsert
		result := tx.Where("resource_id = ?", resource.ResourceID).
			Assign(*resource).
			FirstOrCreate(resource)

		return result.Error
	})
}

func (db *DB) BatchUpsertSearchableResources(resources []SearchableResource) error {
	if len(resources) == 0 {
		return nil
	}

	now := time.Now()
	for i := range resources {
		resources[i].IndexedAt = now
	}

	// Use SQLite UPSERT - conflict on the composite unique index
	return db.Transaction(func(tx *gorm.DB) error {
		for _, res := range resources {
			err := tx.Exec(`
				INSERT INTO searchable_resources (resource_id, cluster, kind, api_version, name, namespace, labels, annotations, description, keywords, category, resource_group, resource_version, created_at, updated_at, indexed_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(cluster, kind, name, namespace, resource_group) DO UPDATE SET
					resource_id = excluded.resource_id,
					api_version = excluded.api_version,
					labels = excluded.labels,
					annotations = excluded.annotations,
					description = excluded.description,
					keywords = excluded.keywords,
					resource_version = excluded.resource_version,
					indexed_at = excluded.indexed_at
			`, res.ResourceID, res.Cluster, res.Kind, res.APIVersion, res.Name, res.Namespace,
				res.Labels, res.Annotations, res.Description, res.Keywords, res.Category,
				res.ResourceGroup, res.ResourceVersion, res.CreatedAt, res.UpdatedAt, res.IndexedAt).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) DeleteSearchableResourcesByCluster(cluster string) error {
	return db.DB.Where("cluster = ?", cluster).Delete(&SearchableResource{}).Error
}

// DeleteSearchableResource deletes a single resource by UID
func (db *DB) DeleteSearchableResource(uid string) error {
	return db.DB.Where("resource_id = ?", uid).Delete(&SearchableResource{}).Error
}

// Indexing status

func (db *DB) GetIndexingStatus(cluster string) (*IndexingStatus, error) {
	var status IndexingStatus
	err := db.DB.Where("cluster = ?", cluster).First(&status).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &status, err
}

func (db *DB) UpdateIndexingStatus(status *IndexingStatus) error {
	status.UpdatedAt = time.Now()

	return db.DB.Where("cluster = ?", status.Cluster).
		Assign(*status).
		FirstOrCreate(status).Error
}

// Search history

func (db *DB) SaveSearchHistory(history *SearchHistory) error {
	db.DB.Where("name = ? AND kind = ? AND namespace = ? AND cluster = ?",
		history.Name, history.Kind, history.Namespace, history.Cluster).
		Delete(&SearchHistory{})
	history.CreatedAt = time.Now()
	return db.DB.Create(history).Error
}

func (db *DB) GetRecentSearches(limit int) ([]SearchHistory, error) {
	var searches []SearchHistory
	err := db.DB.Order("created_at DESC").Limit(limit).Find(&searches).Error
	return searches, err
}

// Cleanup old search history
func (db *DB) CleanupOldSearchHistory(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days)
	return db.DB.Where("created_at < ?", cutoff).Delete(&SearchHistory{}).Error
}
