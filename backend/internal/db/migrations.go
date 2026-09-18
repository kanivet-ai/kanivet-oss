package db

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/utils"
)

// schemaVersion is recorded in SQLite's user_version pragma. Bump it and add a
// step to runVersionedMigrations for any one-time change AutoMigrate cannot
// express: dropping indexes, purging rows, reclaiming space. Each step runs
// once per database and is recorded as soon as it completes, so a database
// created by any earlier release is brought forward on its first start after
// an update.
const schemaVersion = 2

// searchRowRetention bounds how long a searchable_resources row survives
// without being refreshed by a watch event or an indexing sweep. Rows are a
// cache of cluster state; one older than this belongs to a cluster nobody has
// opened in a month and is rebuilt on the next open.
const searchRowRetention = 30 * 24 * time.Hour

// eventRetention bounds how long stored Kubernetes events are kept.
const eventRetention = 30 * 24 * time.Hour

func (db *DB) currentSchemaVersion() int {
	var v int
	db.Raw("PRAGMA user_version").Scan(&v)
	return v
}

func (db *DB) setSchemaVersion(v int) error {
	return db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)).Error
}

func (db *DB) runVersionedMigrations() error {
	current := db.currentSchemaVersion()
	if current >= schemaVersion {
		return nil
	}
	steps := []struct {
		version int
		name    string
		run     func() error
	}{
		{1, "drop legacy search indexes, purge stale rows, reclaim space", db.migrateV1},
		{2, "purge search rows whose kind or id predate kind normalization", db.migrateV2},
	}
	for _, step := range steps {
		if current >= step.version {
			continue
		}
		start := time.Now()
		log.Printf("[DB] migrating schema to v%d: %s", step.version, step.name)
		if err := step.run(); err != nil {
			return fmt.Errorf("schema migration v%d (%s): %w", step.version, step.name, err)
		}
		if err := db.setSchemaVersion(step.version); err != nil {
			return err
		}
		current = step.version
		log.Printf("[DB] schema v%d done in %v", step.version, time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// legacySearchIndexes were created by an earlier SearchableResource model whose
// fields used gorm's default index names. The current model indexes the same
// columns under idx_sr_*, so these only doubled the cost of every write.
var legacySearchIndexes = []string{
	"idx_searchable_resources_cluster",
	"idx_searchable_resources_kind",
	"idx_searchable_resources_name",
	"idx_searchable_resources_namespace",
}

func (db *DB) migrateV1() error {
	for _, name := range legacySearchIndexes {
		if err := db.Exec("DROP INDEX IF EXISTS " + name).Error; err != nil {
			return err
		}
	}
	removed, err := db.PurgeStale(time.Now())
	if err != nil {
		return err
	}
	if removed > 0 {
		log.Printf("[DB] purged %d stale rows", removed)
	}
	// auto_vacuum only takes effect once a full VACUUM has rewritten the file,
	// so it is set before the one-time rewrite below. From then on routine
	// purges hand pages back through incremental_vacuum instead of needing
	// another full rewrite.
	db.Exec("PRAGMA auto_vacuum = INCREMENTAL")
	db.reclaimSpace(true)
	return nil
}

// migrateV2 removes search rows that earlier releases wrote in a shape the
// index no longer produces, each of which made one object show up twice in
// search results: rows whose kind is the plural resource name (the LIST sweep
// used to store "deployments" where the watch path stored "Deployment"), rows
// whose resource_id does not follow cluster/group/version/resource/[ns/]name
// (a singular resource segment, or an empty group and version), and every
// kind definition, which is rebuilt in full whenever a cluster is opened.
// Everything removed here is re-indexed by the next sweep or watch event.
func (db *DB) migrateV2() error {
	removed, err := db.PurgeNonCanonicalSearchRows()
	if err != nil {
		return err
	}
	if removed > 0 {
		log.Printf("[DB] purged %d search rows written before kind normalization", removed)
	}
	db.reclaimSpace(false)
	return nil
}

// PurgeNonCanonicalSearchRows deletes searchable_resources rows that are kind
// definitions, carry an all-lowercase kind, or whose resource_id is not the
// canonical ID for their columns. It returns the number of rows removed.
func (db *DB) PurgeNonCanonicalSearchRows() (int64, error) {
	var removed int64
	res := db.Exec(`DELETE FROM searchable_resources WHERE kind = 'KindDefinition' OR kind = lower(kind)`)
	if res.Error != nil {
		return removed, res.Error
	}
	removed += res.RowsAffected

	type row struct {
		ID              uint
		ResourceID      string
		Cluster         string
		Kind            string
		Name            string
		Namespace       string
		ResourceGroup   string
		ResourceVersion string
	}
	const batch = 2000
	var lastID uint
	for {
		var rows []row
		if err := db.Table("searchable_resources").
			Select("id, resource_id, cluster, kind, name, namespace, resource_group, resource_version").
			Where("id > ?", lastID).Order("id ASC").Limit(batch).Scan(&rows).Error; err != nil {
			return removed, err
		}
		if len(rows) == 0 {
			break
		}
		var victims []uint
		for _, r := range rows {
			lastID = r.ID
			want := canonicalSearchResourceID(r.Cluster, r.ResourceGroup, r.ResourceVersion, r.Kind, r.Namespace, r.Name)
			if r.ResourceID != want {
				victims = append(victims, r.ID)
			}
		}
		if len(victims) > 0 {
			res := db.Where("id IN ?", victims).Delete(&SearchableResource{})
			if res.Error != nil {
				return removed, res.Error
			}
			removed += res.RowsAffected
		}
		if len(rows) < batch {
			break
		}
	}
	return removed, nil
}

// canonicalSearchResourceID mirrors storage.BuildResourceID for a row whose
// resource name must be derived from its kind.
func canonicalSearchResourceID(cluster, group, version, kind, namespace, name string) string {
	parts := []string{cluster, group, version, utils.PluralizeKind(kind)}
	if namespace != "" {
		parts = append(parts, namespace)
	}
	parts = append(parts, name)
	return strings.Join(parts, "/")
}
