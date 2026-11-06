package sessionlifecycle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errDraftVersionConflict = errors.New("draft changed in another tab; recover the saved draft before overwriting it")

func draftVersion(metadata []byte) string { return fmt.Sprintf("%x", sha256.Sum256(metadata)) }

// Compare the exact metadata snapshot inside the same transaction as the write.
// A delayed tab cannot overwrite a version it has never read.
func (s draftStore) saveVersion(ctx context.Context, id string, metadata json.RawMessage, version string) (ConversationDraft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConversationDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	d, err := scanDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM conversation_draft WHERE id=?`, id).Scan)
	switch {
	case errors.Is(err, sql.ErrNoRows) && version == "":
		_, err = tx.ExecContext(ctx, `INSERT INTO conversation_draft(id,metadata,created_at) VALUES(?,?,?)`, id, string(metadata), time.Now().UnixMilli())
	case errors.Is(err, sql.ErrNoRows):
		return ConversationDraft{}, errDraftVersionConflict
	case err != nil:
		return ConversationDraft{}, err
	case d.Phase != "draft" || d.Revision == draftVersion(metadata):
		return d, nil
	case d.Revision != version:
		return ConversationDraft{}, errDraftVersionConflict
	default:
		_, err = tx.ExecContext(ctx, `UPDATE conversation_draft SET metadata=? WHERE id=?`, string(metadata), id)
	}
	if err != nil {
		return ConversationDraft{}, err
	}
	d, err = scanDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM conversation_draft WHERE id=?`, id).Scan)
	if err == nil {
		err = tx.Commit()
	}
	return d, err
}
