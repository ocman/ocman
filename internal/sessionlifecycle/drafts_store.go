package sessionlifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/state"
)

var Schema = state.ModuleSchema{
	Module: "sessionlifecycle", Tables: []string{"conversation_draft"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE conversation_draft (
			id TEXT PRIMARY KEY, metadata TEXT NOT NULL, phase TEXT NOT NULL DEFAULT 'draft',
			request TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '',
			steps TEXT NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, submitted_at INTEGER NOT NULL DEFAULT 0
		)`)
		return err
	}},
}

// ConversationDraft keeps the application identity before a native session exists.
// Accepted prompts and setup receipts are durable; browser drafts are only a cache.
type ConversationDraft struct {
	ID          string            `json:"id"`
	Metadata    json.RawMessage   `json:"metadata"`
	Phase       string            `json:"phase"`
	Request     json.RawMessage   `json:"request,omitempty"`
	Result      json.RawMessage   `json:"result,omitempty"`
	Steps       map[string]string `json:"steps"`
	Error       string            `json:"error,omitempty"`
	CreatedAt   int64             `json:"createdAt"`
	SubmittedAt int64             `json:"submittedAt,omitempty"`
	Revision    string            `json:"revision"`
	Archived    bool              `json:"archived"`
}

type draftStore struct{ db *sql.DB }

var errDraftSubmissionConflict = errors.New("the first submission is already persisted")

func scanDraft(scan func(...any) error) (d ConversationDraft, err error) {
	var meta, request, result, steps string
	err = scan(&d.ID, &meta, &d.Phase, &request, &result, &steps, &d.Error, &d.CreatedAt, &d.SubmittedAt, &d.Archived)
	d.Metadata = json.RawMessage(meta)
	d.Revision = draftVersion(d.Metadata)
	if request != "" {
		d.Request = json.RawMessage(request)
	}
	if result != "" {
		d.Result = json.RawMessage(result)
	}
	if err == nil {
		err = json.Unmarshal([]byte(steps), &d.Steps)
	}
	return
}

// Ocman draft IDs are globally allocated, before an owner/native binding exists.
const draftColumns = `id,metadata,phase,request,result,steps,error,created_at,submitted_at,
	conversation_draft.id IN (SELECT session_id FROM archived_session)`

func (s draftStore) get(ctx context.Context, id string) (ConversationDraft, error) {
	return scanDraft(s.db.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM conversation_draft WHERE id=?`, id).Scan)
}

func (s draftStore) list(ctx context.Context) ([]ConversationDraft, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+draftColumns+` FROM conversation_draft ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConversationDraft{}
	for rows.Next() {
		d, err := scanDraft(rows.Scan)
		if err != nil {
			return nil, err
		}
		if d.Phase == "ready" {
			d.Request, d.Result, d.Steps = nil, nil, nil
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s draftStore) save(ctx context.Context, id string, metadata json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversation_draft(id,metadata,created_at) VALUES(?,?,?)
		ON CONFLICT(id) DO UPDATE SET metadata=excluded.metadata WHERE conversation_draft.phase='draft'`,
		id, string(metadata), time.Now().UnixMilli())
	return err
}

// accept commits the complete first payload before the request is acknowledged.
// Replays are reads. Neither a lost response nor a peer tab can start it twice.
func (s draftStore) accept(ctx context.Context, id string, request []byte) (ConversationDraft, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConversationDraft{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE conversation_draft SET phase='accepted', request=?, submitted_at=? WHERE id=? AND phase='draft'`,
		string(request), time.Now().UnixMilli(), id)
	if err != nil {
		return ConversationDraft{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ConversationDraft{}, false, err
	}
	d, err := scanDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM conversation_draft WHERE id=?`, id).Scan)
	if err == nil && n == 0 && string(d.Request) != string(request) {
		err = errDraftSubmissionConflict
	}
	if err == nil {
		err = tx.Commit()
	}
	return d, n == 1, err
}

func (s draftStore) progress(ctx context.Context, id, step, value string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conversation_draft SET steps=json_set(steps,?,?) WHERE id=?`, `$.`+step, value, id)
	return err
}

func (s draftStore) finish(ctx context.Context, id string, result []byte, message string) error {
	if !json.Valid(result) {
		result = nil
	}
	phase := "ready"
	if message != "" {
		phase = "failed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE conversation_draft SET phase=?,result=CASE WHEN ?='' THEN result ELSE ? END,error=? WHERE id=?`, phase, string(result), string(result), message, id)
	return err
}

// Confirm the binding before first delivery so an interruption cannot erase
// the distinction between uncertain allocation and a retryable delivery.
func (s draftStore) confirmed(ctx context.Context, id string, result map[string]any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE conversation_draft SET phase='delivering',result=? WHERE id=?`, string(raw), id)
	return err
}

// A durable completed prompt step proves admission succeeded. Everything else
// stays interrupted. Neither case replays worktrees, allocation or delivery.
func (s draftStore) recover(ctx context.Context) error {
	done := `json_extract(steps,'$.prompt')='done' AND json_extract(CASE WHEN json_valid(result) THEN result ELSE '{}' END,'$.sessionId')=id`
	_, err := s.db.ExecContext(ctx, `UPDATE conversation_draft SET
		phase=CASE WHEN `+done+` THEN 'ready' ELSE 'interrupted' END,
		error=CASE WHEN `+done+` THEN '' ELSE 'Setup was interrupted. The saved prompt has not been replayed.' END,
		result=CASE WHEN `+done+` THEN json_set(result,'$.firstMessageSent',json('true'),'$.firstMessageError','') ELSE result END
		WHERE phase IN ('accepted','starting','delivering')`)
	return err
}
