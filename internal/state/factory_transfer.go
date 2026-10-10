package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// A transfer receipt lives in factory_external_mapping: pending until the
// tracker issue exists, then its URL, so a retry resumes instead of creating
// a second tracker issue.
const (
	factoryTransferSystem  = "tracker"
	factoryTransferKind    = "issue_transfer"
	factoryTransferPending = "pending:"
)

// ReserveFactoryIssueTransfer checks that the Issue can be removed, then holds
// it out of dispatch (deferred) with a pending receipt before any tracker call.
// It returns the tracker URL when an earlier attempt already created it.
func (d *DB) ReserveFactoryIssueTransfer(ctx context.Context, epicID, issueID string, at time.Time) (string, error) {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", model.ErrInvalidGraphMutation, message) }
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var receipt string
	err = tx.QueryRowContext(ctx, `SELECT external_id FROM factory_external_mapping WHERE system = ? AND external_kind = ? AND entity_kind = 'issue' AND entity_id = ?`, factoryTransferSystem, factoryTransferKind, issueID).Scan(&receipt)
	if err == nil {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM factory_issue WHERE id = ? AND epic_id = ? AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)`, issueID, epicID).Scan(&status); err != nil || status != "deferred" {
			return "", invalid("factory issue was already transferred")
		}
		return strings.TrimPrefix(receipt, factoryTransferPending+issueID), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var status, kind string
	err = tx.QueryRowContext(ctx, `SELECT status, kind FROM factory_issue WHERE id = ? AND epic_id = ? AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)`, issueID, epicID).Scan(&status, &kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (status != "open" || (kind != "task" && kind != "implementation"))) {
		return "", invalid("only open task Issues can be transferred")
	}
	if err != nil {
		return "", err
	}
	var pendingGraph bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_plan_gate g JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision WHERE g.epic_id = ? AND g.resolution <> 'approved' AND json_type(p.manifest_json, '$.issues') = 'array')`, epicID).Scan(&pendingGraph); err != nil {
		return "", err
	}
	if pendingGraph {
		return "", invalid("a graph revision awaits approval; decide it before transferring")
	}
	// Dry-run the removal so a ticket that cannot leave the Epic (for example
	// after its project was delivered) never reaches the tracker.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT transfer_check`); err != nil {
		return "", err
	}
	removeErr := mutateFactoryGraphTx(ctx, tx, model.GraphMutation{Action: "delete", EpicID: epicID, IssueID: issueID, Actor: "user"}, false)
	for _, statement := range []string{`ROLLBACK TO transfer_check`, `RELEASE transfer_check`} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return "", err
		}
	}
	if removeErr != nil {
		return "", removeErr
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'deferred', outcome_reason = 'Transferring to the issue tracker' WHERE id = ?`, issueID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_external_mapping (system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at) VALUES (?, ?, ?, 'issue', ?, '{}', ?)`, factoryTransferSystem, factoryTransferKind, factoryTransferPending+issueID, issueID, at.UnixMilli()); err != nil {
		return "", err
	}
	return "", tx.Commit()
}

// RecordFactoryIssueTransfer stores the created tracker issue on the receipt.
func (d *DB) RecordFactoryIssueTransfer(ctx context.Context, issueID, url string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE factory_external_mapping SET external_id = ? WHERE system = ? AND external_kind = ? AND entity_kind = 'issue' AND entity_id = ?`, url, factoryTransferSystem, factoryTransferKind, issueID)
	return err
}

// ReleaseFactoryIssueTransfer undoes a reservation whose tracker call failed.
func (d *DB) ReleaseFactoryIssueTransfer(ctx context.Context, epicID, issueID string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `DELETE FROM factory_external_mapping WHERE system = ? AND external_kind = ? AND external_id = ?`, factoryTransferSystem, factoryTransferKind, factoryTransferPending+issueID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome_reason = '' WHERE id = ? AND epic_id = ? AND status = 'deferred'`, issueID, epicID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CompleteFactoryIssueTransfer links the tracker issue and removes the ticket
// in one transaction.
func (d *DB) CompleteFactoryIssueTransfer(ctx context.Context, epicID, issueID, url string, at time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_comment (issue_id, actor, body, created_at) VALUES (?, 'user', ?, ?)`, issueID, "Transferred to "+url, at.UnixMilli()); err != nil {
		return err
	}
	if err := mutateFactoryGraphTx(ctx, tx, model.GraphMutation{Action: "delete", EpicID: epicID, IssueID: issueID, Actor: "user"}, false); err != nil {
		return err
	}
	return tx.Commit()
}
