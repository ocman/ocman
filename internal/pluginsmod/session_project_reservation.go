package pluginsmod

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessionsvc"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func uncertainSessionCreation(err error) bool {
	var pending *platforms.CreationPendingError
	var cleanup *sessionsvc.ConfiguredSessionCleanupError
	return errors.As(err, &pending) || errors.As(err, &cleanup) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func migratePluginSessionProjectReservations(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE plugin_session_project ADD COLUMN reservation_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE plugin_session_project ADD COLUMN committed INTEGER NOT NULL DEFAULT 1`)
	return err
}

type sessionProjectReservation struct {
	store                            *Store
	pluginID, directory, operationID string
}

// Pending claims fence concurrent creation but never enter the project inventory.
// A claim left by a crash remains unavailable rather than guessing whether a
// session was created. Confirmed failures release only their own operation.
func (d *Store) reserveSessionProject(ctx context.Context, pluginID, operationID, directory, project string) (*sessionProjectReservation, error) {
	if operationID == "" || !filepath.IsAbs(directory) || !filepath.IsAbs(project) || directory == project {
		return nil, plugins.ErrInvalidMessage
	}
	result, err := d.db.ExecContext(ctx, `INSERT INTO plugin_session_project
		(directory,plugin_id,project_directory,reservation_id,committed) VALUES (?,?,?,?,0)
		ON CONFLICT(directory) DO NOTHING`, directory, pluginID, project, operationID)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 1 {
		return &sessionProjectReservation{store: d, pluginID: pluginID, directory: directory, operationID: operationID}, nil
	}
	var owner, target string
	var committed bool
	if err := d.db.QueryRowContext(ctx, `SELECT plugin_id,project_directory,committed FROM plugin_session_project
		WHERE directory=?`, directory).Scan(&owner, &target, &committed); err != nil {
		return nil, err
	}
	if !committed {
		return nil, plugins.ErrUnavailable
	}
	if owner != pluginID || target != project {
		return nil, &plugins.WireError{Category: plugins.ErrorConflict}
	}
	// An existing committed association needs no new claim or cleanup.
	return nil, nil
}

func (r *sessionProjectReservation) commit(ctx context.Context) error {
	if r == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, err := r.store.db.ExecContext(ctx, `UPDATE plugin_session_project SET committed=1,reservation_id=''
		WHERE directory=? AND plugin_id=? AND reservation_id=? AND committed=0`, r.directory, r.pluginID, r.operationID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return plugins.ErrUnavailable
	}
	return nil
}

func (r *sessionProjectReservation) release(ctx context.Context) error {
	if r == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := r.store.db.ExecContext(ctx, `DELETE FROM plugin_session_project
		WHERE directory=? AND plugin_id=? AND reservation_id=? AND committed=0`, r.directory, r.pluginID, r.operationID)
	return err
}
