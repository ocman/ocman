package state

import (
	"database/sql"
	"fmt"
	"time"
)

// Loop is one row in the `loops` table: a self-driving orchestration
// that fires an action on a trigger until a stop condition is met.
//
// TriggerConfig and StopConditions are opaque JSON strings here; the
// internal/loops domain package owns their shape so the schema stays
// stable as new trigger/stop kinds are added (AD-9).
type Loop struct {
	ID             string  `json:"id"`
	Platform       string  `json:"platform"`
	RootSessionID  string  `json:"rootSessionID"`
	ParentLoopID   string  `json:"parentLoopID,omitempty"` // empty for top-level loops
	Directory      string  `json:"directory"`
	ProjectName    string  `json:"projectName"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	CurrentTask    string  `json:"currentTask"`
	Pattern        string  `json:"pattern"`     // pr_address, orchestrator, heartbeat, linear
	TriggerType    string  `json:"triggerType"` // child_complete, schedule, pr_event, turn_complete
	TriggerConfig  string  `json:"-"`           // raw JSON; decoded form is surfaced by the loops domain
	ActionType     string  `json:"actionType"`  // prompt_root, prompt_child, spawn_child, spawn_worktree
	ActionTemplate string  `json:"actionTemplate"`
	StopConditions string  `json:"-"` // raw JSON; decoded form is surfaced by the loops domain
	State          string  `json:"state"`       // active, paused, completed, deleted, errored
	LoopSessionID  string  `json:"loopSessionID,omitempty"` // the loop's dedicated session; empty until first fire
	SessionMode    string  `json:"sessionMode"`             // fresh (new session per iteration) | reuse
	Iteration      int     `json:"iteration"`
	ErrorStreak    int     `json:"errorStreak"`
	TokensUsed     int64   `json:"tokensUsed"`
	CostUSD        float64 `json:"costUSD"`
	LastFiredAt    int64   `json:"lastFiredAt"` // Unix ms of the most recent action; 0 if never fired
	CreatedAt      int64   `json:"createdAt"`
	UpdatedAt      int64   `json:"updatedAt"`
	CompletedAt    int64   `json:"completedAt"` // 0 until terminal
	LastSummary    string  `json:"lastSummary"`
}

// LoopIteration is one row in the `loop_iterations` audit trail. A row
// is created with Outcome="pending" before the action's side effect,
// then updated to "ok"/"error" afterwards (AD-5a idempotency outbox).
type LoopIteration struct {
	ID              int64  `json:"id"`
	LoopID          string `json:"loopID"`
	Seq             int    `json:"seq"`
	FiredAt         int64  `json:"firedAt"`
	StartedAt       int64  `json:"startedAt"`
	CompletedAt     int64  `json:"completedAt"`
	TriggerDetail   string `json:"triggerDetail"`
	RenderedPrompt  string `json:"renderedPrompt"`
	TargetSessionID string `json:"targetSessionID"`
	ChildSessionID  string `json:"childSessionID"`
	Outcome         string `json:"outcome"` // pending, ok, error, skipped
	Summary         string `json:"summary"`
}

// InsertLoop persists a new loop. Callers set CreatedAt/UpdatedAt.
func (d *DB) InsertLoop(l Loop) error {
	_, err := d.db.Exec(`
		INSERT INTO loops
			(id, platform, root_session_id, parent_loop_id, directory,
			 project_name, title, description, current_task, pattern,
			 trigger_type, trigger_config, action_type, action_template,
			 stop_conditions, state, iteration, error_streak, tokens_used,
			 cost_usd, last_fired_at, created_at, updated_at, completed_at,
			 last_summary, loop_session_id, session_mode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		l.ID, l.Platform, l.RootSessionID, nullableString(l.ParentLoopID), l.Directory,
		l.ProjectName, l.Title, l.Description, l.CurrentTask, l.Pattern,
		l.TriggerType, nonEmptyJSON(l.TriggerConfig), l.ActionType, l.ActionTemplate,
		nonEmptyJSON(l.StopConditions), defaultState(l.State), l.Iteration, l.ErrorStreak, l.TokensUsed,
		l.CostUSD, l.LastFiredAt, l.CreatedAt, l.UpdatedAt, nullableInt(l.CompletedAt),
		l.LastSummary, nullableString(l.LoopSessionID), defaultSessionMode(l.SessionMode),
	)
	if err != nil {
		return fmt.Errorf("inserting loop: %w", err)
	}
	return nil
}

// UpdateLoop overwrites the mutable fields of a loop (everything the
// engine advances each tick). created_at/platform/root are immutable
// and not touched. updated_at is set to now.
func (d *DB) UpdateLoop(l Loop) error {
	_, err := d.db.Exec(`
		UPDATE loops SET
			directory       = ?,
			project_name    = ?,
			title           = ?,
			description     = ?,
			current_task    = ?,
			pattern         = ?,
			trigger_type    = ?,
			trigger_config  = ?,
			action_type     = ?,
			action_template = ?,
			stop_conditions = ?,
			state           = ?,
			iteration       = ?,
			error_streak    = ?,
			tokens_used     = ?,
			cost_usd        = ?,
			last_fired_at   = ?,
			updated_at      = ?,
			completed_at    = ?,
			last_summary    = ?,
			loop_session_id = ?,
			session_mode    = ?
		WHERE id = ?
	`,
		l.Directory, l.ProjectName, l.Title, l.Description, l.CurrentTask, l.Pattern,
		l.TriggerType, nonEmptyJSON(l.TriggerConfig), l.ActionType, l.ActionTemplate,
		nonEmptyJSON(l.StopConditions), defaultState(l.State), l.Iteration, l.ErrorStreak, l.TokensUsed,
		l.CostUSD, l.LastFiredAt, time.Now().UnixMilli(), nullableInt(l.CompletedAt),
		l.LastSummary, nullableString(l.LoopSessionID), defaultSessionMode(l.SessionMode), l.ID,
	)
	if err != nil {
		return fmt.Errorf("updating loop: %w", err)
	}
	return nil
}

// SetLoopState transitions a loop to a new state, stamping completed_at
// when the new state is terminal. last_summary is updated when non-empty.
func (d *DB) SetLoopState(id, newState, summary string) error {
	terminal := newState == "completed" || newState == "deleted" || newState == "errored"
	completedAt := int64(0)
	if terminal {
		completedAt = time.Now().UnixMilli()
	}
	_, err := d.db.Exec(`
		UPDATE loops SET
			state        = ?,
			completed_at = CASE WHEN ? != 0 THEN ? ELSE completed_at END,
			last_summary = CASE WHEN ? != '' THEN ? ELSE last_summary END,
			updated_at   = ?
		WHERE id = ?
	`, newState, completedAt, completedAt, summary, summary, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("setting loop state: %w", err)
	}
	return nil
}

// GetLoop returns a single loop by ID, or an error wrapping
// sql.ErrNoRows when not found.
func (d *DB) GetLoop(id string) (*Loop, error) {
	row := d.db.QueryRow(loopSelectSQL+` WHERE id = ?`, id)
	l, err := scanLoop(row)
	if err != nil {
		return nil, fmt.Errorf("getting loop: %w", err)
	}
	return l, nil
}

// ListLoops returns loops, newest first, excluding soft-deleted ones.
// When rootSessionID is non-empty it filters to that root; when directory
// is non-empty it filters to that directory. Both empty returns all
// (non-deleted) loops.
func (d *DB) ListLoops(rootSessionID, directory string) ([]Loop, error) {
	q := loopSelectSQL + ` WHERE state != 'deleted'`
	var args []interface{}
	if rootSessionID != "" {
		q += ` AND root_session_id = ?`
		args = append(args, rootSessionID)
	}
	if directory != "" {
		q += ` AND directory = ?`
		args = append(args, directory)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing loops: %w", err)
	}
	defer rows.Close()
	return scanLoops(rows)
}

// ListActiveLoops returns loops whose state is 'active', oldest first.
// Used by the engine tick to find loops needing evaluation.
func (d *DB) ListActiveLoops() ([]Loop, error) {
	rows, err := d.db.Query(loopSelectSQL + ` WHERE state = 'active' ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing active loops: %w", err)
	}
	defer rows.Close()
	return scanLoops(rows)
}

// ListLoopsByParent returns the direct child loops of parentLoopID,
// newest first. Used for sub-loop usage rollup and nested rendering.
func (d *DB) ListLoopsByParent(parentLoopID string) ([]Loop, error) {
	rows, err := d.db.Query(loopSelectSQL+` WHERE parent_loop_id = ? AND state != 'deleted' ORDER BY created_at DESC`, parentLoopID)
	if err != nil {
		return nil, fmt.Errorf("listing loops by parent: %w", err)
	}
	defer rows.Close()
	return scanLoops(rows)
}

// InsertLoopIteration persists a new iteration row and returns its
// auto-assigned id. Used by the action dispatcher to create the
// pending-outbox row before a side effect (AD-5a).
func (d *DB) InsertLoopIteration(it LoopIteration) (int64, error) {
	res, err := d.db.Exec(`
		INSERT INTO loop_iterations
			(loop_id, seq, fired_at, started_at, completed_at, trigger_detail,
			 rendered_prompt, target_session_id, child_session_id, outcome, summary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		it.LoopID, it.Seq, it.FiredAt, it.StartedAt, it.CompletedAt, it.TriggerDetail,
		it.RenderedPrompt, it.TargetSessionID, it.ChildSessionID, defaultOutcome(it.Outcome), it.Summary,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting loop iteration: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("loop iteration id: %w", err)
	}
	return id, nil
}

// UpdateLoopIteration updates the outcome of an existing iteration row
// after its side effect completes. Used to close out the pending outbox.
func (d *DB) UpdateLoopIteration(it LoopIteration) error {
	_, err := d.db.Exec(`
		UPDATE loop_iterations SET
			completed_at      = ?,
			target_session_id = ?,
			child_session_id  = ?,
			outcome           = ?,
			summary           = ?
		WHERE id = ?
	`, it.CompletedAt, it.TargetSessionID, it.ChildSessionID, defaultOutcome(it.Outcome), it.Summary, it.ID)
	if err != nil {
		return fmt.Errorf("updating loop iteration: %w", err)
	}
	return nil
}

// ListLoopIterations returns all iterations for a loop, ordered by seq.
func (d *DB) ListLoopIterations(loopID string) ([]LoopIteration, error) {
	rows, err := d.db.Query(`
		SELECT id, loop_id, seq, fired_at, started_at, completed_at,
		       trigger_detail, rendered_prompt, target_session_id,
		       child_session_id, outcome, summary
		FROM loop_iterations
		WHERE loop_id = ?
		ORDER BY seq ASC
	`, loopID)
	if err != nil {
		return nil, fmt.Errorf("listing loop iterations: %w", err)
	}
	defer rows.Close()
	var out []LoopIteration
	for rows.Next() {
		var it LoopIteration
		if err := rows.Scan(
			&it.ID, &it.LoopID, &it.Seq, &it.FiredAt, &it.StartedAt, &it.CompletedAt,
			&it.TriggerDetail, &it.RenderedPrompt, &it.TargetSessionID,
			&it.ChildSessionID, &it.Outcome, &it.Summary,
		); err != nil {
			return nil, fmt.Errorf("scanning loop iteration: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading loop iterations: %w", err)
	}
	return out, nil
}

// ListChildSessionsByLoop returns child sessions spawned by a loop,
// newest first. Used by the usage aggregator and workflow view.
func (d *DB) ListChildSessionsByLoop(loopID string) ([]ChildSession, error) {
	rows, err := d.db.Query(`
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary, loop_id
		FROM child_sessions
		WHERE loop_id = ?
		ORDER BY created_at DESC
	`, loopID)
	if err != nil {
		return nil, fmt.Errorf("listing child sessions by loop: %w", err)
	}
	defer rows.Close()
	return scanChildSessions(rows)
}

// loopSelectSQL is the shared column list for loop reads.
const loopSelectSQL = `
	SELECT id, platform, root_session_id, parent_loop_id, directory,
	       project_name, title, description, current_task, pattern,
	       trigger_type, trigger_config, action_type, action_template,
	       stop_conditions, state, iteration, error_streak, tokens_used,
	       cost_usd, last_fired_at, created_at, updated_at, completed_at,
	       last_summary, loop_session_id, session_mode
	FROM loops`

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanLoop(row rowScanner) (*Loop, error) {
	var l Loop
	var parentLoopID, loopSessionID sql.NullString
	var completedAt sql.NullInt64
	if err := row.Scan(
		&l.ID, &l.Platform, &l.RootSessionID, &parentLoopID, &l.Directory,
		&l.ProjectName, &l.Title, &l.Description, &l.CurrentTask, &l.Pattern,
		&l.TriggerType, &l.TriggerConfig, &l.ActionType, &l.ActionTemplate,
		&l.StopConditions, &l.State, &l.Iteration, &l.ErrorStreak, &l.TokensUsed,
		&l.CostUSD, &l.LastFiredAt, &l.CreatedAt, &l.UpdatedAt, &completedAt,
		&l.LastSummary, &loopSessionID, &l.SessionMode,
	); err != nil {
		return nil, err
	}
	l.ParentLoopID = parentLoopID.String
	l.LoopSessionID = loopSessionID.String
	if completedAt.Valid {
		l.CompletedAt = completedAt.Int64
	}
	return &l, nil
}

func scanLoops(rows *sql.Rows) ([]Loop, error) {
	var out []Loop
	for rows.Next() {
		l, err := scanLoop(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning loop: %w", err)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading loops: %w", err)
	}
	return out, nil
}

func nullableInt(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func nonEmptyJSON(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

func defaultState(s string) string {
	if s == "" {
		return "active"
	}
	return s
}

func defaultSessionMode(s string) string {
	if s == "" {
		return "fresh"
	}
	return s
}

func defaultOutcome(s string) string {
	if s == "" {
		return "pending"
	}
	return s
}
