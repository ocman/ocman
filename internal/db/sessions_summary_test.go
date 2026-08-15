package db

import (
	"errors"
	"reflect"
	"testing"
)

// seedSummaryFixture builds a database that exercises every input the
// session-list projection reads: token/cost aggregation, message counts,
// the last-message role/finish/error fields, the synthesized-terminal
// part heuristic, subagents with and without a parent_id, and sessions
// with no messages at all.
//
// It is deliberately shared by the equivalence tests below so a new
// column added to the projection is covered by both.
func seedSummaryFixture(t *testing.T, d *DB) {
	t.Helper()

	// Plain session: two user prompts, two assistant replies with
	// tokens/cost, last message finished cleanly.
	insertSession(t, d, "ses-plain", "Plain", "/repo/a", 1000, 5000)
	insertMessage(t, d, "m1", "ses-plain", 1100, map[string]any{"role": "user"})
	insertMessage(t, d, "m2", "ses-plain", 1200, map[string]any{
		"role":   "assistant",
		"tokens": map[string]any{"input": 10, "output": 20},
		"cost":   0.25,
	})
	insertMessage(t, d, "m3", "ses-plain", 1300, map[string]any{"role": "user"})
	insertMessage(t, d, "m4", "ses-plain", 1400, map[string]any{
		"role":   "assistant",
		"tokens": map[string]any{"input": 5, "output": 7},
		"cost":   0.5,
		"finish": "stop",
	})
	insertPart(t, d, "p1", "m4", "ses-plain", 1400, map[string]any{"type": "step-start"})

	// Errored session: last message carries a full error envelope so the
	// notice-normalizer columns are non-empty.
	insertSession(t, d, "ses-error", "Errored", "/repo/b", 2000, 6000)
	insertMessage(t, d, "e1", "ses-error", 2100, map[string]any{"role": "user"})
	insertMessage(t, d, "e2", "ses-error", 2200, map[string]any{
		"role": "assistant",
		"error": map[string]any{
			"name": "ProviderAuthError",
			"data": map[string]any{"message": "bad api key"},
		},
	})

	// Shell session: one part, no step-start, nothing running — this is
	// the synthesized-terminal shape.
	insertSession(t, d, "ses-shell", "Shell", "/repo/a", 3000, 7000)
	insertMessage(t, d, "s1", "ses-shell", 3100, map[string]any{"role": "assistant"})
	insertPart(t, d, "sp1", "s1", "ses-shell", 3100, map[string]any{"type": "text"})

	// Running tool: a part in the running state must defeat the
	// synthesized-terminal heuristic.
	insertSession(t, d, "ses-running", "Running", "/repo/b", 3500, 7500)
	insertMessage(t, d, "r1", "ses-running", 3600, map[string]any{"role": "assistant"})
	insertPart(t, d, "rp1", "r1", "ses-running", 3600, map[string]any{
		"type":  "tool",
		"state": map[string]any{"status": "running"},
	})

	// Subagent with a real parent_id: kept by the query, filtered later
	// by FilterInactiveChildren (not by GetSessions itself).
	insertSubagent(t, d, "ses-child", "ses-plain", "Explore (@explore subagent)", "/repo/a", 4000, 8000)
	insertMessage(t, d, "c1", "ses-child", 4100, map[string]any{"role": "user"})

	// Parentless subagent: dropped by GetSessions after the query.
	insertSession(t, d, "ses-orphan", "Judge (auto-approve subagent)", "/repo/a", 4500, 8500)

	// Empty session: no messages, no parts.
	insertSession(t, d, "ses-empty", "Empty", "/repo/c", 5000, 9000)

	// Shared session with a summary and share URL, so the nullable
	// columns are non-NULL for at least one row.
	insertSession(t, d, "ses-shared", "Shared", "/repo/c", 5500, 9500)
	if _, err := d.db.Exec(
		`UPDATE session SET summary_additions = 3, summary_deletions = 4, summary_files = 2, share_url = 'https://share/x' WHERE id = 'ses-shared'`,
	); err != nil {
		t.Fatalf("updating shared session: %v", err)
	}
}

// TestGetSessionSummary_MatchesGetSessionsRow is the equivalence oracle
// for incremental refresh: recomputing one session must produce exactly
// the row a clean full scan produces for it. If the single-session read
// ever diverges from the list query, this fails.
func TestGetSessionSummary_MatchesGetSessionsRow(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedSummaryFixture(t, d)

	full, err := d.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(full) == 0 {
		t.Fatal("fixture produced no sessions")
	}

	for _, want := range full {
		got, err := d.GetSessionSummary(want.ID)
		if err != nil {
			t.Fatalf("GetSessionSummary(%s): %v", want.ID, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("GetSessionSummary(%s)\n got = %#v\nwant = %#v", want.ID, got, want)
		}
	}
}

// TestGetSessionSummary_MissingRowIsDistinguishable pins that an absent
// session is reported as ErrSessionNotFound rather than a zero row, so
// the snapshot cache can tell "deleted" from "unchanged".
func TestGetSessionSummary_MissingRowIsDistinguishable(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedSummaryFixture(t, d)

	_, err := d.GetSessionSummary("ses-does-not-exist")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSessionSummary(missing) error = %v, want ErrSessionNotFound", err)
	}
}

// TestGetSessionSummary_ParentlessSubagentIsMissing pins that the
// post-query filter GetSessions applies also applies here: a row the
// list never contains must be reported missing, not returned, or the
// cache would merge in a session the full scan drops.
func TestGetSessionSummary_ParentlessSubagentIsMissing(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedSummaryFixture(t, d)

	_, err := d.GetSessionSummary("ses-orphan")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSessionSummary(parentless subagent) error = %v, want ErrSessionNotFound", err)
	}
}
