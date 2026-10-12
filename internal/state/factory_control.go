package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// An Epic's control session is the one conversation where the user steers
// Factory, and control actions are the changes it proposes. Both live in
// factory_external_mapping, so they need no table of their own.
const (
	factoryControlKind       = "control_session"
	factoryControlActionKind = "control_action"
)

// FactoryControlSession is the Epic's control conversation.
type FactoryControlSession struct {
	EpicID          string `json:"epicId"`
	Platform        string `json:"platform"`
	SessionID       string `json:"sessionId"`
	ProtocolVersion int    `json:"protocolVersion,omitempty"`
}

// GetFactoryControlSession returns the Epic's control session, if any.
func (d *DB) GetFactoryControlSession(ctx context.Context, epicID string) (FactoryControlSession, bool, error) {
	var metadata string
	err := d.db.QueryRowContext(ctx, `SELECT metadata_json FROM factory_external_mapping
		WHERE system = 'factory' AND external_kind = ? AND entity_kind = 'epic' AND entity_id = ?`, factoryControlKind, epicID).Scan(&metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return FactoryControlSession{}, false, nil
	}
	if err != nil {
		return FactoryControlSession{}, false, err
	}
	var control FactoryControlSession
	return control, true, json.Unmarshal([]byte(metadata), &control)
}

// SetFactoryControlSession records (or replaces) the Epic's control session.
// It adopts nothing once the Epic is closed, so a session launched while the
// Epic was closing is never left unarchived: adopted reports which happened.
func (d *DB) SetFactoryControlSession(ctx context.Context, control FactoryControlSession, at time.Time) (adopted bool, err error) {
	metadata, err := json.Marshal(control)
	if err != nil {
		return false, err
	}
	result, err := d.db.ExecContext(ctx, `INSERT INTO factory_external_mapping (system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at)
		SELECT 'factory', ?, ?, 'epic', ?, ?, ? WHERE EXISTS (SELECT 1 FROM factory_epic WHERE id = ? AND status IN ('open', 'paused'))
		ON CONFLICT (system, external_kind, entity_kind, entity_id) DO UPDATE SET metadata_json = excluded.metadata_json, created_at = excluded.created_at`,
		factoryControlKind, "control:"+control.EpicID, control.EpicID, string(metadata), at.UnixMilli(), control.EpicID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// A successful change invalidates alternative proposals based on the old state.
func (d *DB) DismissFactoryControlAlternatives(ctx context.Context, epicID, exceptID string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE factory_external_mapping SET metadata_json = json_set(metadata_json, '$.status', 'dismissed', '$.result', 'Superseded by a confirmed Epic change; inspect the current Epic before proposing again')
		WHERE system = 'factory' AND external_kind = ? AND json_extract(metadata_json, '$.epicId') = ? AND entity_id <> ? AND json_extract(metadata_json, '$.status') = 'pending'`, factoryControlActionKind, epicID, exceptID)
	return err
}

// archiveFactoryControlSessionTx archives a closing Epic's control session
// the same way its attempt sessions are archived.
func archiveFactoryControlSessionTx(ctx context.Context, tx *sql.Tx, now int64, epicID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
		SELECT json_extract(metadata_json, '$.platform'), json_extract(metadata_json, '$.sessionId'), 9223372036854775807, ?
		FROM factory_external_mapping WHERE system = 'factory' AND external_kind = ? AND entity_kind = 'epic' AND entity_id = ?
		ON CONFLICT(platform, session_id) DO UPDATE SET session_time_updated = excluded.session_time_updated, archived_at = excluded.archived_at`,
		now, factoryControlKind, epicID)
	return err
}

// FactoryControlAction is a change an agent proposed for an Epic. Only the
// user's confirmation on its card runs it.
type FactoryControlAction struct {
	ID      string          `json:"id"`
	EpicID  string          `json:"epicId"`
	Action  string          `json:"action"`
	Args    json.RawMessage `json:"args"`
	Summary string          `json:"summary"`
	Status  string          `json:"status"` // pending, confirmed, dismissed, done, failed
	Result  string          `json:"result,omitempty"`
}

// CreateFactoryControlAction records a pending proposal.
func (d *DB) CreateFactoryControlAction(ctx context.Context, action FactoryControlAction, at time.Time) error {
	action.Status = "pending"
	metadata, err := json.Marshal(action)
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `INSERT INTO factory_external_mapping (system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at)
		VALUES ('factory', ?, ?, 'control_action', ?, ?, ?)`, factoryControlActionKind, action.ID, action.ID, string(metadata), at.UnixMilli())
	return err
}

// GetFactoryControlAction returns one proposal of the Epic.
func (d *DB) GetFactoryControlAction(ctx context.Context, epicID, id string) (FactoryControlAction, bool, error) {
	var metadata string
	err := d.db.QueryRowContext(ctx, `SELECT metadata_json FROM factory_external_mapping
		WHERE system = 'factory' AND external_kind = ? AND entity_kind = 'control_action' AND entity_id = ? AND json_extract(metadata_json, '$.epicId') = ?`, factoryControlActionKind, id, epicID).Scan(&metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return FactoryControlAction{}, false, nil
	}
	if err != nil {
		return FactoryControlAction{}, false, err
	}
	var action FactoryControlAction
	return action, true, json.Unmarshal([]byte(metadata), &action)
}

// TransitionFactoryControlAction moves a proposal from one status to another
// and reports whether it was in the expected status, so a double click can
// never run it twice.
func (d *DB) TransitionFactoryControlAction(ctx context.Context, epicID, id, from, to, result string) (bool, error) {
	outcome, err := d.db.ExecContext(ctx, `UPDATE factory_external_mapping SET metadata_json = json_set(metadata_json, '$.status', ?, '$.result', ?)
		WHERE system = 'factory' AND external_kind = ? AND entity_kind = 'control_action' AND entity_id = ?
		AND json_extract(metadata_json, '$.epicId') = ? AND json_extract(metadata_json, '$.status') = ?`, to, result, factoryControlActionKind, id, epicID, from)
	if err != nil {
		return false, err
	}
	n, err := outcome.RowsAffected()
	return n == 1, err
}
