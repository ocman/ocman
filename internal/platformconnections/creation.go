package platformconnections

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms/identity"
)

var ErrCreationConflict = errors.New("connection creation receipt changed")

func migrateCreationConnections(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE platform_connection_creation (
		request_key TEXT PRIMARY KEY NOT NULL CHECK(length(request_key)>0),
		session_id TEXT NOT NULL UNIQUE REFERENCES platform_connection_session(session_id)
	);`)
	return err
}

type CreationRoute struct {
	Route
	SessionID string
}

func creationSession(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key string) (string, string, error) {
	var id, connection string
	err := query.QueryRowContext(ctx, `SELECT c.session_id,s.connection_id
		FROM platform_connection_creation c JOIN platform_connection_session s ON s.session_id=c.session_id
		WHERE c.request_key=?`, key).Scan(&id, &connection)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return id, connection, err
}

// AdmitCreation pins a request key, canonical session ID and connection in one
// transaction before session-catalogue reservation or native side effects.
// Empty selection on replay keeps the original account. Payload replay checks
// still belong to the session catalogue; this receipt grants no execution by
// itself. The caller must verify authority for any supplied existing session ID.
func (r *Router) AdmitCreation(ctx context.Context, key, sessionID string, ref Reference) (CreationRoute, error) {
	if r.store == nil || strings.TrimSpace(key) == "" || len(key) > 512 || strings.ContainsFunc(key, unicode.IsControl) || (sessionID != "" && !identity.IsSessionID(sessionID)) {
		return CreationRoute{}, ErrInvalid
	}
	id, previous, err := creationSession(ctx, r.store.db, key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return CreationRoute{}, err
	}
	if err == nil {
		if sessionID != "" && sessionID != id {
			return CreationRoute{}, ErrCreationConflict
		}
		if ref == (Reference{}) {
			ref.ConnectionID = previous
		}
	}
	route, err := r.Resolve(ctx, ref)
	if err != nil {
		return CreationRoute{}, err
	}
	if previous != "" && previous != route.Connection.ID {
		return CreationRoute{}, ErrSessionConflict
	}
	if id != "" {
		return CreationRoute{Route: route, SessionID: id}, nil
	}
	id, err = r.store.reserveCreation(ctx, key, sessionID, route.Connection.ID)
	if err != nil {
		return CreationRoute{}, err
	}
	return CreationRoute{Route: route, SessionID: id}, nil
}

func (s *Store) reserveCreation(ctx context.Context, key, requestedID, connectionID string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	id, previous, err := creationSession(ctx, tx, key)
	if err == nil {
		if previous != connectionID {
			return "", ErrSessionConflict
		}
		if requestedID != "" && requestedID != id {
			return "", ErrCreationConflict
		}
		return id, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	id = requestedID
	if id == "" {
		id = "ocm_" + uuid.NewString()
	}
	// A canonical identity cannot be assigned a second creation request key.
	var occupied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_connection_creation WHERE session_id=?)`, id).Scan(&occupied); err != nil {
		return "", err
	}
	if occupied {
		return "", ErrCreationConflict
	}
	if err := bindSession(ctx, tx, id, connectionID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO platform_connection_creation(request_key,session_id) VALUES(?,?)`, key, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
