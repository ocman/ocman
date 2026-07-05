package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SetAutoApprove enables or disables the auto-approve judge for a
// specific (platform, session) pair. Overwrites any existing row.
func (d *DB) SetAutoApprove(platform, sessionID string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := d.db.Exec(`
		INSERT INTO session_auto_approve (platform, session_id, enabled, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			enabled    = excluded.enabled,
			updated_at = excluded.updated_at
	`, platform, sessionID, val, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("setting auto-approve: %w", err)
	}
	return nil
}

// GetAutoApprove returns whether the auto-approve judge is explicitly
// enabled for the given session. The second return value is false when
// no per-session override exists (caller should use the global default).
func (d *DB) GetAutoApprove(platform, sessionID string) (enabled bool, exists bool, err error) {
	var val int
	err = d.db.QueryRow(
		`SELECT enabled FROM session_auto_approve WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	).Scan(&val)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("getting auto-approve: %w", err)
	}
	return val != 0, true, nil
}

// ApprovedPermission holds the data for one auto-approved permission,
// used to re-inject the notice into the conversation thread on reload.
//
// Reasoning is the LLM judge's one-line conclusion (the "reasoning"
// field of the JSON it emits). Empty when the judge response could
// not be parsed or pre-dates schema v11.
type ApprovedPermission struct {
	PermissionID   string
	PermissionText string
	Patterns       []string
	JudgeSessionID string
	Reasoning      string
	ApprovedAt     int64
}

// RecordApprovedPermission persists one auto-approved permission for a
// session. Idempotent: repeated calls with the same permission_id
// silently overwrite the existing row.
func (d *DB) RecordApprovedPermission(platform, sessionID string, p ApprovedPermission) error {
	patternsJSON, err := encodePatterns(p.Patterns)
	if err != nil {
		return fmt.Errorf("encoding patterns: %w", err)
	}
	_, err = d.db.Exec(`
		INSERT INTO auto_approved_permission
			(platform, session_id, permission_id, permission_text, patterns_json, judge_session_id, reasoning, approved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(platform, session_id, permission_id) DO UPDATE SET
			permission_text  = excluded.permission_text,
			patterns_json    = excluded.patterns_json,
			judge_session_id = excluded.judge_session_id,
			reasoning        = excluded.reasoning,
			approved_at      = excluded.approved_at
	`, platform, sessionID, p.PermissionID, p.PermissionText, patternsJSON, p.JudgeSessionID, p.Reasoning, p.ApprovedAt)
	if err != nil {
		return fmt.Errorf("recording approved permission: %w", err)
	}
	return nil
}

// ListApprovedPermissions returns all auto-approved permissions for a
// session, ordered by approval time ascending.
func (d *DB) ListApprovedPermissions(platform, sessionID string) ([]ApprovedPermission, error) {
	rows, err := d.db.Query(`
		SELECT permission_id, permission_text, patterns_json, judge_session_id, reasoning, approved_at
		FROM auto_approved_permission
		WHERE platform = ? AND session_id = ?
		ORDER BY approved_at ASC
	`, platform, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing approved permissions: %w", err)
	}
	defer rows.Close()

	var out []ApprovedPermission
	for rows.Next() {
		var p ApprovedPermission
		var patternsJSON string
		if err := rows.Scan(&p.PermissionID, &p.PermissionText, &patternsJSON, &p.JudgeSessionID, &p.Reasoning, &p.ApprovedAt); err != nil {
			return nil, fmt.Errorf("scanning approved permission: %w", err)
		}
		p.Patterns, err = decodePatterns(patternsJSON)
		if err != nil {
			p.Patterns = nil
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading approved permissions: %w", err)
	}
	return out, nil
}

// PromptSection is a user-defined extra section appended to the judge prompt.
// Matches the PromptSection type in internal/server/autoapprove.go.
type PromptSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	// Enabled is a pointer so legacy rows (persisted before this field
	// existed) unmarshal to nil and are treated as enabled.
	Enabled *bool `json:"enabled,omitempty"`
}

// GetPromptSections returns the persisted judge prompt sections, or an
// empty slice if none have been saved yet.
func (d *DB) GetPromptSections() ([]PromptSection, error) {
	var sectionsJSON string
	err := d.db.QueryRow(`SELECT sections_json FROM judge_prompt_sections WHERE id = 1`).Scan(&sectionsJSON)
	if err == sql.ErrNoRows {
		return []PromptSection{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading prompt sections: %w", err)
	}
	var out []PromptSection
	if err := json.Unmarshal([]byte(sectionsJSON), &out); err != nil {
		return []PromptSection{}, nil
	}
	return out, nil
}

// SetPromptSections persists the judge prompt sections, overwriting any
// existing row. Pass an empty slice to clear all custom sections.
func (d *DB) SetPromptSections(sections []PromptSection) error {
	if sections == nil {
		sections = []PromptSection{}
	}
	b, err := json.Marshal(sections)
	if err != nil {
		return fmt.Errorf("encoding prompt sections: %w", err)
	}
	_, err = d.db.Exec(`
		INSERT INTO judge_prompt_sections (id, sections_json, updated_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			sections_json = excluded.sections_json,
			updated_at    = excluded.updated_at
	`, string(b), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("saving prompt sections: %w", err)
	}
	return nil
}

// DefaultJudgeDelayMs is the delay used when no row exists in judge_settings.
const DefaultJudgeDelayMs = 5000

// GetJudgeDelayMs returns the configured delay (ms) the backend waits
// after a permission.asked event before starting the LLM judge.
// Returns defaultJudgeDelayMs when no row has been saved yet.
func (d *DB) GetJudgeDelayMs() (int64, error) {
	var ms int64
	err := d.db.QueryRow(`SELECT delay_ms FROM judge_settings WHERE id = 1`).Scan(&ms)
	if err == sql.ErrNoRows {
		return DefaultJudgeDelayMs, nil
	}
	if err != nil {
		return DefaultJudgeDelayMs, fmt.Errorf("reading judge delay: %w", err)
	}
	return ms, nil
}

// SetJudgeDelayMs persists the judge delay. A value of 0 means no delay
// (judge fires immediately). Negative values are clamped to 0.
func (d *DB) SetJudgeDelayMs(ms int64) error {
	if ms < 0 {
		ms = 0
	}
	_, err := d.db.Exec(`
		INSERT INTO judge_settings (id, delay_ms) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET delay_ms = excluded.delay_ms
	`, ms)
	if err != nil {
		return fmt.Errorf("saving judge delay: %w", err)
	}
	return nil
}

// encodePatterns marshals a string slice to a JSON array string.
func encodePatterns(patterns []string) (string, error) {
	if patterns == nil {
		patterns = []string{}
	}
	b, err := json.Marshal(patterns)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodePatterns unmarshals a JSON array string to a string slice.
func decodePatterns(s string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}
