package state

import "context"

type AgentWait struct {
	SessionID string
	Start     int64
	End       int64
}

// User waits are owner-qualified and observed after the approval decision,
// including permissions with autoapproval disabled. Older permission lifecycle
// records fill in history where available; safe approvals never block the user.
func (d *DB) AgentUserWaits(ctx context.Context, platform string, since, until int64) ([]AgentWait, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT session_id,
		CASE WHEN manually_preempted = 0 AND judge_completed_at > requested_at
			THEN judge_completed_at ELSE requested_at END,
		CASE WHEN resolved_at = 0 THEN ? ELSE resolved_at END
		FROM permission_lifecycle l WHERE platform = ? AND requested_at > 0
		AND requested_at < ? AND (resolved_at = 0 OR resolved_at > ?)
		AND resolution != 'auto-approved'
		AND (resolution IN ('user-once', 'user-always', 'user-rejected', 'cancelled')
			OR evaluation_result IN ('unsafe', 'denylisted', 'error') OR judge_started_at = 0)
		AND NOT EXISTS (SELECT 1 FROM agent_user_wait w WHERE w.platform = l.platform
			AND w.session_id = l.session_id AND w.kind = 'permission' AND w.request_id = l.permission_id)
		UNION ALL SELECT session_id, started_at, CASE WHEN resolved_at = 0 THEN ? ELSE resolved_at END
		FROM agent_user_wait WHERE platform = ? AND started_at > 0 AND started_at < ?
		AND (resolved_at = 0 OR resolved_at > ?)`, until, platform, until, since, until, platform, until, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waits := []AgentWait{}
	for rows.Next() {
		var wait AgentWait
		if err := rows.Scan(&wait.SessionID, &wait.Start, &wait.End); err != nil {
			return nil, err
		}
		if wait.End > wait.Start {
			waits = append(waits, wait)
		}
	}
	return waits, rows.Err()
}

// A resolved request is never reopened by an ask replay or by late observation.
func (d *DB) StartAgentUserWait(ctx context.Context, platform, session, kind, request string, at int64) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO agent_user_wait(platform, session_id, kind, request_id, started_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(platform, session_id, kind, request_id) DO NOTHING`, platform, session, kind, request, at)
	return err
}

func (d *DB) ResolveAgentUserWait(ctx context.Context, platform, session, kind, request string, at int64) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO agent_user_wait(platform, session_id, kind, request_id, resolved_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(platform, session_id, kind, request_id)
		DO UPDATE SET resolved_at = CASE WHEN agent_user_wait.resolved_at = 0 THEN excluded.resolved_at ELSE agent_user_wait.resolved_at END`, platform, session, kind, request, at)
	return err
}

func (d *DB) ResolveSessionUserWaits(ctx context.Context, platform, session string, at int64) error {
	_, err := d.db.ExecContext(ctx, `UPDATE agent_user_wait SET resolved_at = ?
		WHERE platform = ? AND session_id = ? AND resolved_at = 0`, at, platform, session)
	return err
}
