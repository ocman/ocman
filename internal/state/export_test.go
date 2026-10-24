package state

import "database/sql"

// OpenFromSQL wraps an existing *sql.DB in a DB and runs migrations, for
// tests that open an in-memory database directly rather than via Open.
func OpenFromSQL(sqlDB *sql.DB, modules ...ModuleSchema) (*DB, error) {
	d := &DB{db: sqlDB}
	if err := d.init(modules); err != nil {
		return nil, err
	}
	return d, nil
}
