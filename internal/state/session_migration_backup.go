package state

import (
	"context"
	"sync"
)

type sessionMigrationBackupKey struct{}
type sessionMigrationBackups struct {
	mu   sync.Mutex
	done map[*DB]bool
}

// WithSessionMigrationBackup shares one pre-mutation backup across a quiescent,
// replayable cutover's batches and feature migrations. A new run takes a new
// backup; ordinary independent callers retain their existing backup behavior.
func WithSessionMigrationBackup(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionMigrationBackupKey{}, &sessionMigrationBackups{done: map[*DB]bool{}})
}

// BackupSessionReferences snapshots the database before a feature-owned,
// pre-serving session-reference migration. The feature owns its SQL transaction;
// this shares the core's durable backup path and publication guarantees.
func (d *DB) BackupSessionReferences(ctx context.Context) error {
	return d.backupSessionReferences(ctx, "feature-session-keys")
}

func (d *DB) backupSessionReferences(ctx context.Context, label string) error {
	if backups, ok := ctx.Value(sessionMigrationBackupKey{}).(*sessionMigrationBackups); ok {
		backups.mu.Lock()
		defer backups.mu.Unlock()
		if backups.done[d] {
			return nil
		}
		if err := snapshotBeforeMigration(d.db, label); err != nil {
			return err
		}
		backups.done[d] = true
		return nil
	}
	return snapshotBeforeMigration(d.db, label)
}
