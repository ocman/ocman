package state

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	log "github.com/sirupsen/logrus"
)

// ModuleSchema is a feature module's own persistence boundary in state.db:
// the tables it owns and the append-only migrations that build them. The
// module package declares it next to the queries that read those tables;
// the composition root passes every module's schema to Open.
//
// Tables created by the core sequence (schema_version) stay there, with
// their repositories in this package, even when a module is their main
// reader. A module schema only covers tables whose migrations and queries
// both live in the module, so neither side imports the other.
type ModuleSchema struct {
	// Module is the ledger key. It is persisted, so it never changes.
	Module string
	// Tables lists every table the migrations create. Each must exist once
	// they have run, may not be claimed by another module, and may only
	// reference its own or core tables by foreign key.
	Tables []string
	// Migrations are append-only: version n is Migrations[n-1]. Never edit,
	// reorder or remove an entry that has shipped.
	Migrations []func(*sql.Tx) error
}

// SQL returns the state database handle for a module's own repository.
// Use it only for the tables of that module's ModuleSchema.
func (d *DB) SQL() *sql.DB { return d.db }

// migrateModules applies every module's pending migrations after the core
// sequence, as one batch: modules in the given order, versions ascending,
// all in one transaction with the ledger rows, after the same pre-migration
// backup the core sequence takes. Any error rolls the whole batch back.
//
// coreBefore is the core version before this Open migrated it. A fresh
// database (0) needs no backup, and one the core sequence just upgraded
// already has a backup from before both batches.
func migrateModules(db *sql.DB, schemas []ModuleSchema, coreBefore int) error {
	if len(schemas) == 0 {
		return nil
	}
	if coreBefore > latestSchemaVersion {
		return nil // migrate already warned: a newer ocman owns this schema
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS module_schema_version (
		module TEXT NOT NULL,
		version INTEGER NOT NULL,
		applied_at INTEGER NOT NULL,
		PRIMARY KEY (module, version)
	)`); err != nil {
		return fmt.Errorf("creating module_schema_version: %w", err)
	}
	applied := make([]int, len(schemas))
	pending := false
	for i, schema := range schemas {
		if err := db.QueryRow(`SELECT COALESCE(max(version), 0) FROM module_schema_version WHERE module = ?`,
			schema.Module).Scan(&applied[i]); err != nil {
			return fmt.Errorf("reading %s schema version: %w", schema.Module, err)
		}
		if applied[i] > len(schema.Migrations) {
			log.Errorf("state: module %s is at schema v%d but this ocman only understands v%d: "+
				"it was written by a newer ocman; skipping its migrations, upgrade ocman if errors occur",
				schema.Module, applied[i], len(schema.Migrations))
			continue
		}
		pending = pending || applied[i] < len(schema.Migrations)
	}
	if !pending {
		return nil
	}
	if coreBefore == latestSchemaVersion {
		if err := snapshotBeforeMigration(db, fmt.Sprintf("v%d-modules", coreBefore)); err != nil {
			return fmt.Errorf("pre-migration backup: %w", err)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("module migrate begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, schema := range schemas {
		for v := applied[i] + 1; v <= len(schema.Migrations); v++ {
			if err := schema.Migrations[v-1](tx); err != nil {
				return fmt.Errorf("migrate %s to v%d: %w", schema.Module, v, err)
			}
			if _, err := tx.Exec(`INSERT INTO module_schema_version (module, version, applied_at) VALUES (?, ?, ?)`,
				schema.Module, v, time.Now().UnixMilli()); err != nil {
				return fmt.Errorf("recording %s schema v%d: %w", schema.Module, v, err)
			}
		}
	}
	if err := checkModuleTables(tx, schemas); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("module migrate commit: %w", err)
	}
	return nil
}

func validateModuleSchemas(schemas []ModuleSchema) error {
	modules := map[string]bool{}
	tables := map[string]string{}
	for _, schema := range schemas {
		if schema.Module == "" {
			return errors.New("module schema without a module name")
		}
		if modules[schema.Module] {
			return fmt.Errorf("module schema %q registered twice", schema.Module)
		}
		modules[schema.Module] = true
		if len(schema.Tables) > 0 && len(schema.Migrations) == 0 {
			// Nothing would ever be pending, so the table checks never run.
			return fmt.Errorf("module %s declares tables but no migrations", schema.Module)
		}
		for i, migration := range schema.Migrations {
			if migration == nil {
				return fmt.Errorf("module %s migration v%d is nil", schema.Module, i+1)
			}
		}
		for _, table := range schema.Tables {
			if owner, ok := tables[table]; ok {
				return fmt.Errorf("table %s claimed by modules %s and %s", table, owner, schema.Module)
			}
			tables[table] = schema.Module
		}
	}
	return nil
}

// checkModuleTables fails the batch when a module's declared tables do not
// exist or reference another module's tables, which would couple modules
// that may not import each other.
func checkModuleTables(tx *sql.Tx, schemas []ModuleSchema) error {
	owner := map[string]string{}
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			owner[table] = schema.Module
		}
	}
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			var exists bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`,
				table).Scan(&exists); err != nil {
				return fmt.Errorf("checking %s table %s: %w", schema.Module, table, err)
			}
			if !exists {
				return fmt.Errorf("module %s declares table %s but its migrations do not create it", schema.Module, table)
			}
			parents, err := foreignKeyParents(tx, table)
			if err != nil {
				return err
			}
			for _, parent := range parents {
				if o, ok := owner[parent]; ok && o != schema.Module {
					return fmt.Errorf("module %s table %s references %s table %s", schema.Module, table, o, parent)
				}
			}
		}
	}
	return nil
}

func foreignKeyParents(tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.Query(`SELECT "table" FROM pragma_foreign_key_list(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("reading %s foreign keys: %w", table, err)
	}
	defer rows.Close()
	var parents []string
	for rows.Next() {
		var parent string
		if err := rows.Scan(&parent); err != nil {
			return nil, fmt.Errorf("scanning %s foreign keys: %w", table, err)
		}
		if !slices.Contains(parents, parent) {
			parents = append(parents, parent)
		}
	}
	return parents, rows.Err()
}
