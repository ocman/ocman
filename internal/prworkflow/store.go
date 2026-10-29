// Package prworkflow persists session PR links and opt-in background watches.
package prworkflow

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/state"
)

var Schema = state.ModuleSchema{Module: "pr-workflow", Tables: []string{"session_pull_request"}, Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_pull_request (platform TEXT NOT NULL, session_id TEXT NOT NULL, host TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(platform,session_id,host,repo,number))`)
	return err
}}}

type Link struct {
	Platform     string       `json:"platform"`
	SessionID    string       `json:"sessionId"`
	Directory    string       `json:"directory"`
	RemoteID     string       `json:"remoteId"`
	Remote       forge.Remote `json:"remote"`
	Number       int          `json:"number"`
	PR           forge.PR     `json:"pr"`
	Watching     bool         `json:"watching"`
	Snapshot     *Snapshot    `json:"-"`
	Pending      string       `json:"-"`
	Failures     int          `json:"-"`
	NextAt       int64        `json:"-"`
	CommentWakes int          `json:"-"`
	LastError    string       `json:"lastError,omitempty"`
	// Private durable fields are encoded separately, never sent to clients.
}

type persisted struct {
	Link
	Snapshot     *Snapshot
	Pending      string
	Failures     int
	NextAt       int64
	CommentWakes int
}

func (m *Module) put(ctx context.Context, l Link) error {
	if m.d.DB == nil {
		return sql.ErrConnDone
	}
	b, err := json.Marshal(persisted{l, l.Snapshot, l.Pending, l.Failures, l.NextAt, l.CommentWakes})
	if err != nil {
		return err
	}
	_, err = m.d.DB.SQL().ExecContext(ctx, `INSERT INTO session_pull_request VALUES(?,?,?,?,?,?) ON CONFLICT(platform,session_id,host,repo,number) DO UPDATE SET payload=excluded.payload`, l.Platform, l.SessionID, l.Remote.Host, l.Remote.Repo, l.Number, string(b))
	return err
}

func (m *Module) links(ctx context.Context, platform, id string) ([]Link, error) {
	result := []Link{}
	if m.d.DB == nil {
		return result, nil
	}
	query := `SELECT payload FROM session_pull_request`
	args := []any{}
	if platform != "" {
		query += ` WHERE platform=? AND session_id=?`
		args = append(args, platform, id)
	}
	query += ` ORDER BY host,repo,number`
	rows, err := m.d.DB.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var p persisted
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		l := p.Link
		l.Snapshot, l.Pending, l.Failures, l.NextAt, l.CommentWakes = p.Snapshot, p.Pending, p.Failures, p.NextAt, p.CommentWakes
		result = append(result, l)
	}
	return result, rows.Err()
}

func (m *Module) remove(ctx context.Context, l Link) error {
	_, err := m.d.DB.SQL().ExecContext(ctx, `DELETE FROM session_pull_request WHERE platform=? AND session_id=? AND host=? AND repo=? AND number=?`, l.Platform, l.SessionID, l.Remote.Host, l.Remote.Repo, l.Number)
	return err
}
