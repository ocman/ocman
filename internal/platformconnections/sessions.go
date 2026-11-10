package platformconnections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ocman/ocman/internal/platforms/identity"
)

var ErrSessionConflict = errors.New("session belongs to another platform connection")

func migrateSessionConnections(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE platform_connection_session (
		session_id TEXT PRIMARY KEY NOT NULL,
		connection_id TEXT NOT NULL REFERENCES platform_connection(id)
	);
	CREATE INDEX platform_connection_session_connection ON platform_connection_session(connection_id);`)
	return err
}

// BindSession pins a canonical session to an account before native side effects.
// It is idempotent only for the same connection. Removal retains this mapping;
// neither changing the default nor recreating an account can acquire a session.
// The caller must verify owner/source authority in the session catalogue first.
func (s *Store) BindSession(ctx context.Context, sessionID, connectionID string) error {
	if !identity.IsSessionID(sessionID) {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT connection_id FROM platform_connection_session WHERE session_id=?`, sessionID).Scan(&previous)
	if err == nil {
		if previous != connectionID {
			return ErrSessionConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	c, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM platform_connection WHERE id=?`, connectionID))
	if err != nil {
		return err
	}
	if !c.Enabled || c.Removed {
		return ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO platform_connection_session(session_id,connection_id) VALUES(?,?)`, sessionID, connectionID); err != nil {
		return err
	}
	return tx.Commit()
}

// SessionConnection reads only a durable binding, never the current default or
// a kind-based guess. Tombstones remain readable so execution can fail closed.
func (s *Store) SessionConnection(ctx context.Context, sessionID string) (string, error) {
	if !identity.IsSessionID(sessionID) {
		return "", ErrInvalid
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT connection_id FROM platform_connection_session WHERE session_id=?`, sessionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return id, err
}

// ResolveSession routes an existing session only through its pinned connection.
// The caller still verifies its canonical owner and physical source. There is
// no automatic binding of unknown/imported sessions to the current default.
func (r *Router) ResolveSession(ctx context.Context, sessionID string) (Route, error) {
	if r.store == nil {
		return Route{}, ErrInvalid
	}
	id, err := r.store.SessionConnection(ctx, sessionID)
	if err != nil {
		return Route{}, err
	}
	return r.Resolve(ctx, Reference{ConnectionID: id})
}

// AdmitSession selects and persists the account before a caller can create or
// adopt a native session. Empty references on retry honor the durable binding,
// not a changed default. Explicit conflicting references are rejected.
func (r *Router) AdmitSession(ctx context.Context, sessionID string, ref Reference) (Route, error) {
	if r.store == nil {
		return Route{}, ErrInvalid
	}
	previous, err := r.store.SessionConnection(ctx, sessionID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Route{}, err
	}
	if err == nil && ref == (Reference{}) {
		ref.ConnectionID = previous
	}
	route, err := r.Resolve(ctx, ref)
	if err != nil {
		return Route{}, err
	}
	if previous != "" && previous != route.Connection.ID {
		return Route{}, ErrSessionConflict
	}
	if err := r.store.BindSession(ctx, sessionID, route.Connection.ID); err != nil {
		return Route{}, fmt.Errorf("pin session connection: %w", err)
	}
	return route, nil
}
