package factory

import (
	"context"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/state"
)

// These helpers put an issue into deferred and retry states for tests.
func deferFactoryIssue(ctx context.Context, d *state.DB, epicID, issueID, reason string) error {
	return setFactoryIssueState(ctx, d, `status = 'deferred', outcome = '', outcome_reason = ?`, "open", reason, issueID, epicID)
}

func resumeFactoryIssue(ctx context.Context, d *state.DB, epicID, issueID string) error {
	return setFactoryIssueState(ctx, d, `status = 'open', outcome_reason = ''`, "deferred", issueID, epicID)
}

func retryFactoryIssueAt(ctx context.Context, d *state.DB, epicID, issueID string, wakeAt time.Time) error {
	return setFactoryIssueState(ctx, d, `status = 'retry_wait', outcome = '', retry_at = ?, retry_attempts = retry_attempts + 1`, "open", wakeAt.UnixMilli(), issueID, epicID)
}

func setFactoryIssueState(ctx context.Context, d *state.DB, set, from string, args ...any) error {
	result, err := d.SQL().ExecContext(ctx, `UPDATE factory_issue SET `+set+` WHERE id = ? AND epic_id = ? AND status = '`+from+`'`, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed != 1 {
		err = fmt.Errorf("factory issue is not %s", from)
	}
	return err
}
