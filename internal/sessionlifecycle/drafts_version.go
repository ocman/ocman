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

const draftSubmitGracePeriod = 5 * time.Minute

func draftVersion(metadata []byte) string { return fmt.Sprintf("%x", sha256.Sum256(metadata)) }

func hasEmptyDraftText(metadata json.RawMessage) bool {
	var draft struct {
		Text string `json:"text"`
	}
	return json.Unmarshal(metadata, &draft) == nil && draft.Text == ""
}

// Compare the exact metadata snapshot inside the same transaction as the write.
// Only an explicit submission may replace unseen text after the grace period.
func (s draftStore) saveVersion(ctx context.Context, id string, metadata json.RawMessage, version string, submitting bool) (ConversationDraft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConversationDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	d, err := scanDraft(tx.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM conversation_draft WHERE id=?`, id).Scan)
	now := time.Now()
	oldEnoughToSubmit := submitting && d.updatedAt > 0 && d.updatedAt <= now.Add(-draftSubmitGracePeriod).UnixMilli()
	switch {
	case errors.Is(err, sql.ErrNoRows) && version == "":
		_, err = tx.ExecContext(ctx, `INSERT INTO conversation_draft(id,metadata,created_at,updated_at) VALUES(?,?,?,?)`, id, string(metadata), now.UnixMilli(), now.UnixMilli())
	case errors.Is(err, sql.ErrNoRows):
		return ConversationDraft{}, errDraftVersionConflict
	case err != nil:
		return ConversationDraft{}, err
	case d.Phase != "draft" || d.Revision == draftVersion(metadata):
		return d, nil
	case d.Revision != version && !hasEmptyDraftText(d.Metadata) && !oldEnoughToSubmit:
		return ConversationDraft{}, errDraftVersionConflict
	default:
		_, err = tx.ExecContext(ctx, `UPDATE conversation_draft SET metadata=?,updated_at=? WHERE id=?`, string(metadata), now.UnixMilli(), id)
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
