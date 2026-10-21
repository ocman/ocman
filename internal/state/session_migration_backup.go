package state

// BackupSessionReferences snapshots the database before a feature-owned,
// pre-serving session-reference migration. The feature owns its SQL transaction;
// this shares the core's durable backup path and publication guarantees.
func (d *DB) BackupSessionReferences() error {
	return snapshotBeforeMigration(d.db, "feature-session-keys")
}
