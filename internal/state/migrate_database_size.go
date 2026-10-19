package state

import "database/sql"

func migrateHistoryDatabaseSize(tx *sql.Tx) error {
	if err := migrateToV93(tx); err != nil {
		return err
	}
	_, err := tx.Exec(`
		CREATE TABLE database_size_sample_v124 (
			database TEXT NOT NULL CHECK (database IN ('opencode', 'ocman', 'statev2')),
			sampled_at INTEGER NOT NULL,
			size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
			PRIMARY KEY (database, sampled_at)
		);
		INSERT INTO database_size_sample_v124(database, sampled_at, size_bytes)
			SELECT database, sampled_at, size_bytes FROM database_size_sample;
		DROP TABLE database_size_sample;
		ALTER TABLE database_size_sample_v124 RENAME TO database_size_sample;
	`)
	return err
}
