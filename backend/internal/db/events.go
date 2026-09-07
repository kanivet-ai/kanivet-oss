package db

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

type K8sEvent struct {
	ID                       uint      `gorm:"primaryKey" json:"id"`
	Cluster                  string    `gorm:"index:idx_cluster;not null" json:"cluster"`
	UID                      string    `gorm:"index:idx_uid" json:"uid"`
	Name                     string    `gorm:"index:idx_name" json:"name"`
	Namespace                string    `gorm:"index:idx_namespace" json:"namespace"`
	InvolvedObjectUID        string    `gorm:"index:idx_involved_uid" json:"involvedObjectUID"`
	InvolvedObjectName       string    `gorm:"index:idx_involved_name" json:"involvedObjectName"`
	InvolvedObjectNamespace  string    `gorm:"index:idx_involved_namespace" json:"involvedObjectNamespace"`
	InvolvedObjectKind       string    `gorm:"index:idx_involved_kind" json:"involvedObjectKind"`
	InvolvedObjectAPIVersion string    `json:"involvedObjectAPIVersion"`
	Type                     string    `json:"type"`
	Reason                   string    `json:"reason"`
	Message                  string    `json:"message"`
	Count                    int32     `json:"count"`
	FirstTimestamp           time.Time `json:"firstTimestamp"`
	LastTimestamp            time.Time `json:"lastTimestamp"`
	EventTime                time.Time `gorm:"index:idx_event_time" json:"eventTime"`
	SourceComponent          string    `json:"sourceComponent"`
	SourceHost               string    `json:"sourceHost"`
	CreatedAt                time.Time `gorm:"index:idx_created_at" json:"createdAt"`
}

type EventListener struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Cluster       string    `gorm:"uniqueIndex;not null" json:"cluster"`
	IsActive      bool      `gorm:"default:false" json:"isActive"`
	LastEventTime time.Time `json:"lastEventTime"`
	EventCount    int64     `json:"eventCount"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (db *DB) MigrateEvents() error {
	return db.AutoMigrate(&K8sEvent{}, &EventListener{})
}

func (db *DB) CreateOrUpdateEventListener(cluster string) (*EventListener, error) {
	var listener EventListener
	err := db.Where("cluster = ?", cluster).First(&listener).Error

	if err == gorm.ErrRecordNotFound {
		listener = EventListener{
			Cluster:   cluster,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		return &listener, db.Create(&listener).Error
	}

	if err != nil {
		return nil, err
	}

	listener.IsActive = true
	listener.UpdatedAt = time.Now()
	return &listener, db.Save(&listener).Error
}

func (db *DB) GetEventListener(cluster string) (*EventListener, error) {
	var listener EventListener
	err := db.Where("cluster = ?", cluster).First(&listener).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &listener, err
}

var (
	trimMu   sync.Mutex
	lastTrim = map[string]time.Time{}
)

func trimDue(cluster string) bool {
	trimMu.Lock()
	defer trimMu.Unlock()
	if time.Since(lastTrim[cluster]) < time.Minute {
		return false
	}
	lastTrim[cluster] = time.Now()
	return true
}

// StoreEvents upserts by event UID so re-delivered events update their row
// instead of duplicating it, and trims the per-cluster window at most once a
// minute rather than counting the table on every batch.
func (db *DB) StoreEvents(cluster string, events []K8sEvent) error {
	if len(events) == 0 {
		return nil
	}
	now := time.Now()
	byUID := make(map[string]*K8sEvent, len(events))
	var fresh []K8sEvent
	for i := range events {
		e := &events[i]
		e.Cluster, e.CreatedAt = cluster, now
		if e.UID == "" {
			fresh = append(fresh, *e)
			continue
		}
		if prev, dup := byUID[e.UID]; dup {
			*prev = *e
			continue
		}
		byUID[e.UID] = e
	}
	uids := make([]string, 0, len(byUID))
	for uid := range byUID {
		uids = append(uids, uid)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		existing := make(map[string]uint, len(uids))
		for start := 0; start < len(uids); start += 500 {
			var rows []K8sEvent
			if err := tx.Select("id", "uid").Where("cluster = ? AND uid IN ?", cluster, uids[start:min(start+500, len(uids))]).Find(&rows).Error; err != nil {
				return err
			}
			for _, r := range rows {
				existing[r.UID] = r.ID
			}
		}
		for uid, e := range byUID {
			id, ok := existing[uid]
			if !ok {
				fresh = append(fresh, *e)
				continue
			}
			err := tx.Model(&K8sEvent{}).Where("id = ?", id).Updates(map[string]interface{}{
				"type": e.Type, "reason": e.Reason, "message": e.Message, "count": e.Count,
				"first_timestamp": e.FirstTimestamp, "last_timestamp": e.LastTimestamp, "event_time": e.EventTime,
				"source_component": e.SourceComponent, "source_host": e.SourceHost,
			}).Error
			if err != nil {
				return err
			}
		}
		if len(fresh) > 0 {
			if err := tx.Create(&fresh).Error; err != nil {
				return err
			}
			if trimDue(cluster) {
				var eventCount int64
				if err := tx.Model(&K8sEvent{}).Where("cluster = ?", cluster).Count(&eventCount).Error; err != nil {
					return err
				}
				const maxEvents = 10000
				if eventCount > maxEvents {
					if err := tx.Exec(`DELETE FROM k8s_events WHERE id IN (SELECT id FROM k8s_events WHERE cluster = ? ORDER BY event_time ASC, created_at ASC LIMIT ?)`, cluster, eventCount-maxEvents).Error; err != nil {
						return err
					}
				}
			}
		}

		return tx.Model(&EventListener{}).
			Where("cluster = ?", cluster).
			Updates(map[string]interface{}{
				"event_count":     gorm.Expr("event_count + ?", len(fresh)),
				"last_event_time": now,
				"updated_at":      now,
			}).Error
	})
}

func (db *DB) GetEventsForResource(cluster, namespace, name, uid string) ([]K8sEvent, error) {
	var events []K8sEvent

	query := db.Where("cluster = ?", cluster)

	orConditions := db.Where("involved_object_name = ? AND involved_object_namespace = ?", name, namespace)

	if uid != "" {
		orConditions = orConditions.Or("involved_object_uid = ?", uid)
	}

	if namespace == "" {
		orConditions = orConditions.Or("involved_object_name = ? AND involved_object_namespace = ''", name)
	}

	return events, query.Where(orConditions).
		Order("event_time DESC, last_timestamp DESC").
		Limit(20).
		Find(&events).Error
}

func (db *DB) GetEventsForResourceByKind(cluster, namespace, name, kind, apiVersion string) ([]K8sEvent, error) {
	var events []K8sEvent

	query := db.Where("cluster = ?", cluster).
		Where("involved_object_name = ?", name).
		Where("involved_object_kind = ?", kind)

	if namespace != "" {
		query = query.Where("involved_object_namespace = ?", namespace)
	}

	if apiVersion != "" {
		query = query.Where("involved_object_api_version = ?", apiVersion)
	}

	return events, query.
		Order("event_time DESC, last_timestamp DESC").
		Limit(20).
		Find(&events).Error
}

func (db *DB) GetClusterEvents(cluster string, limit int) ([]K8sEvent, error) {
	var events []K8sEvent
	if limit <= 0 {
		limit = 1000
	}
	err := db.Where("cluster = ?", cluster).
		Order("last_timestamp DESC, event_time DESC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

func (db *DB) CleanupOldEvents(cluster string, olderThan time.Time) error {
	return db.Where("cluster = ? AND event_time < ?", cluster, olderThan).Delete(&K8sEvent{}).Error
}

func (db *DB) GetEventCount(cluster string) (int64, error) {
	var count int64
	return count, db.Model(&K8sEvent{}).Where("cluster = ?", cluster).Count(&count).Error
}

func (db *DB) DeactivateEventListener(cluster string) error {
	return db.Model(&EventListener{}).
		Where("cluster = ?", cluster).
		Updates(map[string]interface{}{
			"is_active":  false,
			"updated_at": time.Now(),
		}).Error
}

func (db *DB) GetActiveEventListeners() ([]EventListener, error) {
	var listeners []EventListener
	return listeners, db.Where("is_active = ?", true).Find(&listeners).Error
}
