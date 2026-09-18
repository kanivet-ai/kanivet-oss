package db

import (
	"fmt"
	"log"
	"time"
)

// schemaVersion is recorded in SQLite's user_version pragma. Bump it and add a
// step to runVersionedMigrations for any one-time change AutoMigrate cannot
// express: dropping indexes, purging rows, reclaiming space. Each step runs
// once per database and is recorded as soon as it completes, so a database
// created by any earlier release is brought forward on its first start after
// an update.
const schemaVersion = 1

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
