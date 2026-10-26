package state

import "database/sql"

// Allow each platform to add storage series without a schema change.
func migratePlatformStorage(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE database_size_sample_v126 (
			database TEXT NOT NULL CHECK (database <> ''),
			sampled_at INTEGER NOT NULL,
			size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
			PRIMARY KEY (database, sampled_at)
		);
		INSERT INTO database_size_sample_v126 SELECT database, sampled_at, size_bytes FROM database_size_sample;
		DROP TABLE database_size_sample;
		ALTER TABLE database_size_sample_v126 RENAME TO database_size_sample;
	`)
	return err
}
