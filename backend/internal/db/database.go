package db

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DB struct {
	*gorm.DB
}

type ClusterGroup struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Name         string `gorm:"not null;uniqueIndex:idx_name_not_deleted,where:is_deleted=0" json:"name"`
	Description  string `json:"description"`
	IsPredefined bool   `gorm:"default:false" json:"isPredefined"`
	IsDeleted    bool   `gorm:"default:false" json:"isDeleted"`
	Order        int    `gorm:"column:sort_order;default:0" json:"order"`
}

type ClusterAssignment struct {
	ID          uint         `gorm:"primaryKey" json:"id"`
	ClusterName string       `gorm:"uniqueIndex;not null" json:"clusterName"`
	GroupID     uint         `gorm:"index;not null" json:"groupId"`
	Group       ClusterGroup `gorm:"constraint:OnDelete:CASCADE;" json:"group,omitempty"`
}

// ClusterAlias allows users to give custom names to clusters
type ClusterAlias struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	ClusterName string `gorm:"uniqueIndex;not null" json:"clusterName"`
	Alias       string `gorm:"not null" json:"alias"`
}

// ClusterMetricsSettings stores per-cluster metrics-provider overrides — most
// importantly the Mimir tenant ID (X-Scope-OrgID) since multi-tenant Mimir
// returns empty results without it. Stored per cluster so different EKS
// accounts can have different tenant configs.
type ClusterMetricsSettings struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	ClusterName    string `gorm:"uniqueIndex;not null" json:"clusterName"`
	MimirTenant    string `json:"mimirTenant"`
	MimirService   string `json:"mimirService"`
	MimirNamespace string `json:"mimirNamespace"`
	UpdatedAt      int64  `json:"updatedAt"`
}

type SSOSession struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	StartURL  string `gorm:"uniqueIndex;not null" json:"startUrl"`
	Region    string `gorm:"not null" json:"region"`
	Label     string `json:"label"`
	ExpiresAt int64  `json:"expiresAt"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type SSOActiveAccount struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	StartURL    string `gorm:"not null" json:"startUrl"`
	AccountID   string `gorm:"not null" json:"accountId"`
	AccountName string `json:"accountName"`
	ProfileName string `json:"profileName"`
	RoleName    string `json:"roleName"`
	ExpiresAt   int64  `json:"expiresAt"`
}

func New() (*DB, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	dbDir := filepath.Join(homeDir, ".kanivet")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dbDir, "kanivet.db")
	dsn := "file:" + dbPath + "?cache=shared&mode=rwc&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	// Optimize SQLite for better performance
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Set connection pool settings - SQLite only supports one writer but can have multiple readers
	sqlDB.SetMaxOpenConns(10) // Allow multiple readers
	sqlDB.SetMaxIdleConns(5)

	// Execute PRAGMA statements for better performance
	db.Exec("PRAGMA cache_size = -128000")       // 128MB cache (increased from 64MB)
	db.Exec("PRAGMA temp_store = MEMORY")
	db.Exec("PRAGMA mmap_size = 536870912")      // 512MB memory-mapped I/O (increased from 256MB)
	db.Exec("PRAGMA page_size = 8192")           // Larger page size for better performance
	db.Exec("PRAGMA locking_mode = NORMAL")      // Allow concurrent access
	db.Exec("PRAGMA read_uncommitted = true")    // Allow dirty reads for better concurrency

	if err := db.AutoMigrate(&ClusterGroup{}, &ClusterAssignment{}, &ClusterAlias{}, &SSOSession{}, &SSOActiveAccount{}, &ClusterMetricsSettings{}); err != nil {
		return nil, err
	}

	dbInstance := &DB{db}

	// Migrate search tables
	if err := dbInstance.MigrateSearch(); err != nil {
		return nil, err
	}

	// Migrate event tables
	if err := dbInstance.MigrateEvents(); err != nil {
		return nil, err
	}

	// List snapshots are a pure cache: a failed migration must degrade the
	// feature, never abort startup into a crash loop on the user's DB.
	if err := dbInstance.MigrateSnapshots(); err != nil {
		log.Printf("[DB] snapshot table migration failed, list snapshots disabled: %v", err)
	}

	// Create or update predefined groups
	predefinedGroups := []struct {
		name        string
		description string
		order       int
	}{
		{"dev", "Development environments", 1},
		{"qa", "Quality Assurance environments", 2},
		{"stg", "Staging environments", 3},
		{"prod", "Production environments", 4},
		{"lab", "Lab/Experimental environments", 5},
	}

	for _, pg := range predefinedGroups {
		var existingGroup ClusterGroup
		err := db.Where("name = ?", pg.name).First(&existingGroup).Error

		if err == gorm.ErrRecordNotFound {
			group := &ClusterGroup{
				Name:         pg.name,
				Description:  pg.description,
				IsPredefined: true,
				IsDeleted:    false,
				Order:        pg.order,
			}
			if err := db.Create(group).Error; err != nil {
				continue
			}
		} else if err == nil && !existingGroup.IsDeleted {
			if existingGroup.IsPredefined {
				db.Model(&existingGroup).Updates(map[string]interface{}{
					"sort_order":  pg.order,
					"description": pg.description,
				})
			}
		}
	}

	// Fix order for any user-created groups
	var userGroups []ClusterGroup
	if err := db.Where("is_deleted = ? AND is_predefined = ?", false, false).Order("id").Find(&userGroups).Error; err == nil {
		nextOrder := 6
		for _, g := range userGroups {
			if g.Order < 6 || g.Order == 0 {
				db.Model(&g).Update("sort_order", nextOrder)
				nextOrder++
			}
		}
	}

	return dbInstance, nil
}

func (db *DB) CreateGroup(name, description string) (*ClusterGroup, error) {
	// Check if a soft-deleted group with this name exists
	var existingGroup ClusterGroup
	if err := db.DB.Where("name = ? AND is_deleted = ?", name, true).First(&existingGroup).Error; err == nil {
		// Restore the soft-deleted group
		var maxOrder int
		db.DB.Model(&ClusterGroup{}).Where("is_deleted = ?", false).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)

		updates := map[string]interface{}{
			"is_deleted":  false,
			"description": description,
			"sort_order":  maxOrder + 1,
		}
		if err := db.DB.Model(&existingGroup).Updates(updates).Error; err != nil {
			return nil, err
		}
		return &existingGroup, nil
	}

	var maxOrder int
	db.DB.Model(&ClusterGroup{}).Where("is_deleted = ?", false).Select("COALESCE(MAX(sort_order), 5)").Scan(&maxOrder)

	group := &ClusterGroup{
		Name:         name,
		Description:  description,
		Order:        maxOrder + 1,
		IsPredefined: false,
	}
	if err := db.DB.Create(group).Error; err != nil {
		return nil, err
	}
	return group, nil
}

func (db *DB) GetGroups() ([]ClusterGroup, error) {
	var groups []ClusterGroup
	if err := db.DB.Where("is_deleted = ?", false).Order("sort_order ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	return groups, nil
}

func (db *DB) UpdateGroup(id uint, name, description string) error {
	return db.DB.Model(&ClusterGroup{}).Where("id = ?", id).Updates(ClusterGroup{
		Name:        name,
		Description: description,
	}).Error
}

func (db *DB) UpdateGroupOrder(groupOrders []struct {
	ID    uint
	Order int
}) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, groupOrder := range groupOrders {
			if err := tx.Model(&ClusterGroup{}).Where("id = ? AND is_deleted = ?", groupOrder.ID, false).Update("sort_order", groupOrder.Order).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) DeleteGroup(id uint) error {
	// Mark as deleted instead of hard delete for predefined groups
	var group ClusterGroup
	if err := db.DB.First(&group, id).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if group.IsPredefined {
			if err := tx.Model(&group).Update("is_deleted", true).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Delete(&ClusterGroup{}, id).Error; err != nil {
				return err
			}
		}

		var remainingGroups []ClusterGroup
		if err := tx.Where("is_deleted = ?", false).Order("sort_order ASC").Find(&remainingGroups).Error; err != nil {
			return err
		}

		for i, g := range remainingGroups {
			if err := tx.Model(&g).Update("sort_order", i+1).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

func (db *DB) AssignClusterToGroup(clusterName string, groupID uint) error {
	var assignment ClusterAssignment
	result := db.DB.Where("cluster_name = ?", clusterName).First(&assignment)

	if result.Error == gorm.ErrRecordNotFound {
		assignment = ClusterAssignment{
			ClusterName: clusterName,
			GroupID:     groupID,
		}
		return db.DB.Create(&assignment).Error
	}

	assignment.GroupID = groupID
	return db.DB.Save(&assignment).Error
}

func (db *DB) RemoveClusterFromGroup(clusterName string) error {
	return db.DB.Where("cluster_name = ?", clusterName).Delete(&ClusterAssignment{}).Error
}

func (db *DB) GetClusterGroup(clusterName string) (*ClusterGroup, error) {
	var assignment ClusterAssignment
	err := db.DB.Preload("Group").Where("cluster_name = ?", clusterName).First(&assignment).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &assignment.Group, nil
}

func (db *DB) GetClustersByGroup() (map[string][]string, error) {
	var assignments []ClusterAssignment
	if err := db.DB.Preload("Group").Find(&assignments).Error; err != nil {
		return nil, err
	}

	result := make(map[string][]string)
	for _, a := range assignments {
		result[a.Group.Name] = append(result[a.Group.Name], a.ClusterName)
	}

	return result, nil
}

func (db *DB) GetAllClusterAssignments() (map[string]uint, error) {
	var assignments []ClusterAssignment
	if err := db.DB.Find(&assignments).Error; err != nil {
		return nil, err
	}

	result := make(map[string]uint)
	for _, a := range assignments {
		result[a.ClusterName] = a.GroupID
	}

	return result, nil
}

// SetClusterAlias sets or updates an alias for a cluster
func (db *DB) SetClusterAlias(clusterName, alias string) error {
	var existing ClusterAlias
	result := db.DB.Where("cluster_name = ?", clusterName).First(&existing)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new alias
		newAlias := ClusterAlias{
			ClusterName: clusterName,
			Alias:       alias,
		}
		return db.DB.Create(&newAlias).Error
	}

	if result.Error != nil {
		return result.Error
	}

	// Update existing alias
	existing.Alias = alias
	return db.DB.Save(&existing).Error
}

// GetClusterAlias returns the alias for a cluster, or empty string if not set
func (db *DB) GetClusterAlias(clusterName string) (string, error) {
	var alias ClusterAlias
	err := db.DB.Where("cluster_name = ?", clusterName).First(&alias).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return alias.Alias, nil
}

// GetAllClusterAliases returns a map of cluster name to alias
func (db *DB) GetAllClusterAliases() (map[string]string, error) {
	var aliases []ClusterAlias
	if err := db.DB.Find(&aliases).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, a := range aliases {
		result[a.ClusterName] = a.Alias
	}

	return result, nil
}

// DeleteClusterAlias removes the alias for a cluster
func (db *DB) DeleteClusterAlias(clusterName string) error {
	return db.DB.Where("cluster_name = ?", clusterName).Delete(&ClusterAlias{}).Error
}

func (db *DB) GetClusterMetricsSettings(clusterName string) (*ClusterMetricsSettings, error) {
	var s ClusterMetricsSettings
	if err := db.DB.Where("cluster_name = ?", clusterName).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (db *DB) SetClusterMimirTenant(clusterName, tenant string) error {
	now := time.Now().Unix()
	var s ClusterMetricsSettings
	err := db.DB.Where("cluster_name = ?", clusterName).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		s = ClusterMetricsSettings{ClusterName: clusterName, MimirTenant: tenant, UpdatedAt: now}
		return db.DB.Create(&s).Error
	}
	if err != nil {
		return err
	}
	s.MimirTenant = tenant
	s.UpdatedAt = now
	return db.DB.Save(&s).Error
}

func (db *DB) SetClusterMimirService(clusterName, namespace, service string) error {
	now := time.Now().Unix()
	var s ClusterMetricsSettings
	err := db.DB.Where("cluster_name = ?", clusterName).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		s = ClusterMetricsSettings{ClusterName: clusterName, MimirService: service, MimirNamespace: namespace, UpdatedAt: now}
		return db.DB.Create(&s).Error
	}
	if err != nil {
		return err
	}
	s.MimirService = service
	s.MimirNamespace = namespace
	s.UpdatedAt = now
	return db.DB.Save(&s).Error
}

func (db *DB) GetSSOSessions() ([]SSOSession, error) {
	var sessions []SSOSession
	if err := db.DB.Order("created_at DESC").Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}

func (db *DB) GetSSOSession(startURL string) (*SSOSession, error) {
	var session SSOSession
	err := db.DB.Where("start_url = ?", startURL).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (db *DB) SaveSSOSession(session *SSOSession) error {
	var existing SSOSession
	err := db.DB.Where("start_url = ?", session.StartURL).First(&existing).Error
	if err == nil {
		session.ID = existing.ID
		session.CreatedAt = existing.CreatedAt
		if session.Label == "" {
			session.Label = existing.Label
		}
		return db.DB.Save(session).Error
	}
	return db.DB.Create(session).Error
}

func (db *DB) UpdateSSOSessionLabel(startURL, label string) error {
	return db.DB.Model(&SSOSession{}).Where("start_url = ?", startURL).Update("label", label).Error
}

func (db *DB) UpdateSSOSessionExpiry(startURL string, expiresAt int64) error {
	return db.DB.Model(&SSOSession{}).Where("start_url = ?", startURL).Updates(map[string]interface{}{
		"expires_at": expiresAt,
		"updated_at": time.Now().UnixMilli(),
	}).Error
}

func (db *DB) DeleteSSOSession(startURL string) error {
	return db.DB.Where("start_url = ?", startURL).Delete(&SSOSession{}).Error
}

func (db *DB) GetSSOActiveAccount() (*SSOActiveAccount, error) {
	var account SSOActiveAccount
	err := db.DB.First(&account).Error
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (db *DB) SetSSOActiveAccount(account *SSOActiveAccount) error {
	if err := db.DB.Where("1 = 1").Delete(&SSOActiveAccount{}).Error; err != nil {
		return err
	}
	if account == nil {
		return nil
	}
	return db.DB.Create(account).Error
}

func (db *DB) ClearSSOActiveAccount() error {
	return db.DB.Where("1 = 1").Delete(&SSOActiveAccount{}).Error
}
