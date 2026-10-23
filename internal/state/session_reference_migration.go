package state

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/NoUseFreak/ocman/internal/startup"
)

// These are core-owned associations. Plugin tables have their own migration,
// including compatibility handling for in-flight execution correlations.
var sessionReferenceColumns = []struct{ table, column, owner string }{
	{"agent_user_wait", "session_id", "platform"}, {"archived_session", "session_id", "platform"},
	{"artifact", "session_id", "platform"}, {"auto_approved_permission", "session_id", "platform"},
	{"factory_attempt", "session_id", "session_platform"},
	{"loops", "root_session_id", "platform"}, {"loops", "loop_session_id", "platform"},
	{"permission_lifecycle", "session_id", "platform"}, {"pinned_session", "session_id", "platform"},
	{"plugin_conversation", "session_id", "platform_id"},
	{"prompt_schedule", "session_id", "platform"}, {"queued_message", "session_id", "platform"},
	{"routine", "session_id", "platform"}, {"routine_run", "session_id", "platform"}, {"routine_run", "target_session_id", "platform"},
	{"seen_session", "session_id", "platform"}, {"session_auto_approve", "session_id", "platform"},
	{"session_commit", "session_id", "platform"}, {"session_interruption", "session_id", "platform"},
	{"session_replacement_candidate", "session_id", "platform"}, {"share_link", "session_id", "platform"},
	{"workflow_node_attempt", "session_id", "platform"},
}

// SessionReferences inventories declared core references for one exact routing
// owner. It does not infer owners for blank legacy platform fields. The cutover
// coordinator must resolve those separately rather than adopting another
// machine's colliding ID. Opaque tool data and operation IDs are not references.
func (d *DB) SessionReferences(ctx context.Context, platform string) ([]string, error) {
	if platform == "" {
		return nil, fmt.Errorf("session reference owner is required")
	}
	return d.sessionReferences(ctx, platform)
}

// UnattributedSessionReferences is migration-only. Its references must remain
// quarantined until their owner is known; an empty owner never means local.
func (d *DB) UnattributedSessionReferences(ctx context.Context) ([]string, error) {
	return d.sessionReferences(ctx, "")
}

func (d *DB) sessionReferences(ctx context.Context, platform string) ([]string, error) {
	set := map[string]bool{}
	collect := func(query string, args ...any) error {
		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id sql.NullString
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if id.Valid && id.String != "" {
				set[id.String] = true
			}
		}
		return rows.Err()
	}
	for _, ref := range sessionReferenceColumns {
		if err := collect("SELECT "+ref.column+" FROM "+ref.table+" WHERE "+ref.owner+"=?", platform); err != nil {
			return nil, err
		}
	}
	for _, column := range []string{"target_session_id", "child_session_id"} {
		if err := collect("SELECT "+column+" FROM loop_iterations WHERE loop_id IN (SELECT id FROM loops WHERE platform=?)", platform); err != nil {
			return nil, err
		}
	}
	for _, ref := range sessionJSONReferences {
		if err := collect("SELECT json_extract("+ref.column+",'$.sessionId') FROM "+ref.table+" WHERE "+ref.scope+" AND json_valid("+ref.column+") AND COALESCE(json_extract("+ref.column+",'$.platform'),'')=?", platform); err != nil {
			return nil, err
		}
	}
	for _, column := range []string{"baseline_json", "stop_baseline_json"} {
		if err := collect("SELECT j.key FROM session_replacement r,json_each(r."+column+") j WHERE r.platform=?", platform); err != nil {
			return nil, err
		}
	}
	if platform == "opencode" {
		if err := collect("SELECT judge_session_id FROM auto_approved_permission"); err != nil {
			return nil, err
		}
	}
	rows, err := d.db.QueryContext(ctx, `SELECT entity_key FROM unarchived_entity WHERE kind='session' AND remote_id='local'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var packed string
		if err := rows.Scan(&packed); err != nil {
			rows.Close()
			return nil, err
		}
		if owner, id, ok := strings.Cut(packed, "\x00"); ok && owner == platform && id != "" {
			set[id] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// RekeySessionReferences preserves core state while adopting owner-allocated
// identities. Call only before workers and request handling start. A backup is
// taken before mutation. Colliding durable records fail and roll back instead
// of dropping a queue item, approval or recovery receipt.
func (d *DB) RekeySessionReferences(ctx context.Context, platform string, identities map[string]string) error {
	if platform == "" {
		return fmt.Errorf("session reference owner is required")
	}
	return d.rekeySessionReferences(ctx, platform, identities)
}

func (d *DB) rekeySessionReferences(ctx context.Context, platform string, identities map[string]string) error {
	keys, err := validateSessionRekey(identities)
	if err != nil {
		return err
	}
	refs, err := d.sessionReferences(ctx, platform)
	if err != nil {
		return err
	}
	affected := false
	for _, id := range refs {
		if next := identities[id]; next != "" && next != id {
			affected = true
			break
		}
	}
	if !affected {
		return nil
	}
	startup.Report(ctx, "Backing up session references", 0, 0)
	if err := d.backupSessionReferences(ctx, "canonical-session-keys"); err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var snapshotTrigger string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='trigger' AND name='routine_run_snapshot_immutable'`).Scan(&snapshotTrigger); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER routine_run_snapshot_immutable`); err != nil {
		return err
	}
	if err := rekeyReferenceTables(ctx, tx, platform, keys, identities); err != nil {
		return err
	}
	startup.Report(ctx, "Committing session references", 0, 0)
	if err := rekeyReplacementBaselines(ctx, tx, platform, identities); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, snapshotTrigger); err != nil {
		return err
	}
	return tx.Commit()
}

func validateSessionRekey(identities map[string]string) ([]string, error) {
	keys := make([]string, 0, len(identities))
	targets := map[string]bool{}
	for old, id := range identities {
		if old == "" || id == "" || targets[id] {
			return nil, fmt.Errorf("invalid or colliding session identity mapping")
		}
		targets[id] = true
		if old != id {
			keys = append(keys, old)
		}
	}
	for _, old := range keys {
		if next, ok := identities[identities[old]]; ok && next != identities[old] {
			return nil, fmt.Errorf("chained session identity mapping")
		}
	}
	sort.Strings(keys)
	return keys, nil
}
