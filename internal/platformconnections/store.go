package platformconnections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/state"
)

var (
	ErrUninitialized = errors.New("platform connection collection is not initialized")
	ErrNotFound      = errors.New("platform connection not found")
	ErrUnavailable   = errors.New("platform connection is disabled or removed")
	ErrInvalid       = errors.New("invalid platform connection")
	ErrLimit         = errors.New("platform connection limit reached")
)

const maxConnections = 64

// Connection separates account identity from the platform kind and display name.
// LegacyKind maps old platform-keyed references without changing source/session IDs.
// Directory is immutable, owner-resolved configuration metadata, not a login grant.
// An empty directory is allowed only for unbound migrated legacy configurations.
type Connection struct {
	ID, Kind, Name, Configuration, Directory string
	Enabled, Removed                         bool
	LegacyKind                               string
}

type Input struct {
	Kind, Name, Configuration, Directory string
	Enabled                              bool
}

type Collection struct {
	Initialized, OnboardingComplete bool
	DefaultID                       string
	Connections                     []Connection
}

type Store struct{ db *sql.DB }

func New(db *state.DB) *Store { return &Store{db: db.SQL()} }

const columns = `id,kind,name,configuration,directory,enabled,removed,COALESCE(legacy_kind,'')`

func scan(row interface{ Scan(...any) error }) (Connection, error) {
	var c Connection
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.Configuration, &c.Directory, &c.Enabled, &c.Removed, &c.LegacyKind)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return c, err
}

// Get includes tombstones so retained references never resolve to a sibling.
func (s *Store) Get(ctx context.Context, id string) (Connection, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM platform_connection WHERE id=?`, id))
}

// Read distinguishes an absent collection from an intentionally empty one.
// Its metadata and active rows come from one consistent snapshot.
func (s *Store) Read(ctx context.Context) (Collection, error) {
	result := Collection{Connections: []Connection{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx, `SELECT onboarding_complete,COALESCE(default_id,'') FROM platform_connection_collection WHERE id=1`).Scan(&result.OnboardingComplete, &result.DefaultID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Initialized = true
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM platform_connection WHERE removed=0 ORDER BY rowid`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return result, err
		}
		result.Connections = append(result.Connections, c)
	}
	return result, rows.Err()
}

// Legacy returns the original mapping even after removal. New connections can
// never acquire that mapping, including when they share its kind or name.
func (s *Store) Legacy(ctx context.Context, kind string) (Connection, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM platform_connection WHERE legacy_kind=?`, kind))
}

func validate(input Input, allowUnbound bool) (Input, error) {
	switch input.Kind {
	case identity.OpenCode, "claudecode", "codex":
	default:
		return input, fmt.Errorf("%w: unknown kind", ErrInvalid)
	}
	var err error
	input.Name, err = validateName(input.Name)
	if err != nil {
		return input, err
	}
	if input.Configuration != "existing" && input.Configuration != "isolated" {
		return input, fmt.Errorf("%w: unknown configuration mode", ErrInvalid)
	}
	if allowUnbound && input.Directory == "" {
		return input, nil
	}
	if !filepath.IsAbs(input.Directory) || strings.ContainsFunc(input.Directory, unicode.IsControl) {
		return input, fmt.Errorf("%w: configuration directory must be an owner-local absolute path", ErrInvalid)
	}
	input.Directory = filepath.Clean(input.Directory)
	return input, nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 160 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("%w: name must contain 1 to 160 bytes without control characters", ErrInvalid)
	}
	return name, nil
}

// Create stores an owner-prepared configuration. No filesystem or credential
// operation happens here. Setup reservations and readiness belong to the owner.
func (s *Store) Create(ctx context.Context, input Input) (Connection, error) {
	input, err := validate(input, false)
	if err != nil {
		return Connection{}, err
	}
	c := Connection{ID: "cnx_" + uuid.NewString(), Kind: input.Kind, Name: input.Name,
		Configuration: input.Configuration, Directory: input.Directory, Enabled: input.Enabled}
	result, err := s.db.ExecContext(ctx, `INSERT INTO platform_connection(id,kind,name,configuration,directory,enabled)
		SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM platform_connection_collection WHERE id=1)
		AND (SELECT count(*) FROM platform_connection WHERE removed=0)<?`, c.ID, c.Kind, c.Name, c.Configuration, c.Directory, c.Enabled, maxConnections)
	if err != nil {
		return Connection{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 0 {
		return c, err
	}
	var initialized bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_connection_collection WHERE id=1)`).Scan(&initialized); err != nil {
		return Connection{}, err
	}
	if !initialized {
		return Connection{}, ErrUninitialized
	}
	return Connection{}, ErrLimit
}

// Rename cannot change a connection's account, kind or source binding.
func (s *Store) Rename(ctx context.Context, id, name string) error {
	name, err := validateName(name)
	if err != nil {
		return err
	}
	return affected(s.db.ExecContext(ctx, `UPDATE platform_connection SET name=? WHERE id=? AND removed=0`, name, id))
}

// SetEnabled changes intent only; enabling is never proof of setup readiness.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return affected(s.db.ExecContext(ctx, `UPDATE platform_connection SET enabled=? WHERE id=? AND removed=0`, enabled, id))
}

// Remove records a tombstone after the owner has drained active work. It does
// not stop a runtime, remove credentials or delete history. The saved default
// and legacy mapping stay pinned to this ID, rather than selecting a sibling.
func (s *Store) Remove(ctx context.Context, id string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE platform_connection SET removed=1,enabled=0 WHERE id=?`, id))
}

// SetDefault stores an explicit preference. The owner must verify readiness
// before calling it, and must recheck readiness on every execution admission.
func (s *Store) SetDefault(ctx context.Context, id string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE platform_connection_collection SET default_id=? WHERE id=1
		AND EXISTS(SELECT 1 FROM platform_connection WHERE id=? AND removed=0 AND enabled=1)`, id, id))
}

// Default returns only the exact preferred connection, never a fallback. It
// checks persisted availability, not runtime readiness or owner connectivity.
func (s *Store) Default(ctx context.Context) (Connection, error) {
	c, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM platform_connection
		WHERE id=(SELECT default_id FROM platform_connection_collection WHERE id=1)`))
	if err == nil && (!c.Enabled || c.Removed) {
		err = ErrUnavailable
	}
	return c, err
}

func (s *Store) CompleteOnboarding(ctx context.Context) error {
	return affected(s.db.ExecContext(ctx, `UPDATE platform_connection_collection SET onboarding_complete=1 WHERE id=1`))
}

func affected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
