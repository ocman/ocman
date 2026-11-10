package platformconnections

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
)

func migrateConnectionSources(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE platform_connection_source (
		kind TEXT NOT NULL,
		source_key TEXT NOT NULL CHECK(length(source_key)>0),
		connection_id TEXT NOT NULL REFERENCES platform_connection(id),
		PRIMARY KEY(kind,source_key)
	)`)
	return err
}

// BindSource records physical provenance independently of the mutable default.
// A physical source cannot move to another account, even after removal.
func (s *Store) BindSource(ctx context.Context, id, kind, source string) error {
	if source == "" {
		return ErrInvalid
	}
	c, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if c.Kind != kind || c.Removed {
		return ErrInvalid
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO platform_connection_source(kind,source_key,connection_id)
		VALUES(?,?,?) ON CONFLICT(kind,source_key) DO NOTHING`, kind, source, id)
	if err != nil {
		return err
	}
	bound, err := s.SourceConnection(ctx, kind, source)
	if err != nil {
		return err
	}
	if bound != id {
		return ErrAlreadyBound
	}
	return nil
}

func (s *Store) SourceConnection(ctx context.Context, kind, source string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT connection_id FROM platform_connection_source WHERE kind=? AND source_key=?`, kind, source).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return id, err
}

func (s *Store) SourceKeys(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_key FROM platform_connection_source WHERE connection_id=? ORDER BY source_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// LegacySourceImportAllowed proves the original pre-named, single-account
// namespace still has no same-kind sibling, including tombstones. Once a new
// account exists, unidentified historical sources require explicit provenance.
func (s *Store) LegacySourceImportAllowed(ctx context.Context, id string) (bool, error) {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(legacy_kind=kind,0) AND (SELECT count(*) FROM platform_connection b WHERE b.kind=a.kind)=1 FROM platform_connection a WHERE id=? AND removed=0`, id).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return allowed, err
}

func (r *Router) ResolveSource(ctx context.Context, kind, source string) (Route, error) {
	id, err := r.store.SourceConnection(ctx, kind, source)
	if err != nil {
		return Route{}, err
	}
	return r.Resolve(ctx, Reference{ConnectionID: id})
}

// ResolveReadSource is a retained-history lookup, never execution admission.
func (r *Router) ResolveReadSource(ctx context.Context, kind, source string) (platforms.Platform, error) {
	id, err := r.store.SourceConnection(ctx, kind, source)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	p := r.bound[id]
	r.mu.RUnlock()
	if p == nil {
		return nil, ErrUnavailable
	}
	return p, nil
}

type RetainedConnection struct {
	Connection Connection
	SourceKey  string
}

// RetainedConnections includes tombstones with immutable physical provenance.
func (s *Store) RetainedConnections(ctx context.Context) ([]RetainedConnection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.kind,c.name,c.configuration,c.directory,c.enabled,c.removed,COALESCE(c.legacy_kind,''),s.source_key
		FROM platform_connection c JOIN platform_connection_source s ON s.connection_id=c.id ORDER BY c.rowid,s.source_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetainedConnection
	for rows.Next() {
		var row RetainedConnection
		c := &row.Connection
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Configuration, &c.Directory, &c.Enabled, &c.Removed, &c.LegacyKind, &row.SourceKey); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *Router) BindRead(ctx context.Context, id string, p platforms.Platform) error {
	c, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if p == nil || p.ID() != platforms.ID(c.Kind) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.bound[id]; exists {
		return ErrAlreadyBound
	}
	r.bound[id] = p
	return nil
}
