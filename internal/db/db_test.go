package db

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestDB creates an in-memory SQLite database with the OpenCode schema
// and returns a *DB suitable for testing. The caller should defer db.Close().
func openTestDB(t *testing.T) *DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	_, err = sqlDB.Exec(`
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT '',
			parent_id TEXT,
			title TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL DEFAULT '',
			time_created INTEGER NOT NULL DEFAULT 0,
			time_updated INTEGER NOT NULL DEFAULT 0,
			summary_additions INTEGER,
			summary_deletions INTEGER,
			summary_files INTEGER,
			share_url TEXT
		);
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);
	`)
	if err != nil {
		sqlDB.Close()
		t.Fatalf("creating schema: %v", err)
	}
	return &DB{db: sqlDB}
}

func insertSession(t *testing.T, db *DB, id, title, dir string, created, updated int64) {
	t.Helper()
	_, err := db.db.Exec(
		`INSERT INTO session (id, project_id, title, directory, time_created, time_updated) VALUES (?, '', ?, ?, ?, ?)`,
		id, title, dir, created, updated,
	)
	if err != nil {
		t.Fatalf("inserting session %s: %v", id, err)
	}
}

// insertSubagent inserts a subagent session whose parent_id points at the
// given parent. Title follows the OpenCode "(<name> subagent)" convention so
// the existing GetSessions filter still hides the row from the main listing.
func insertSubagent(t *testing.T, db *DB, id, parentID, title, dir string, created, updated int64) {
	t.Helper()
	_, err := db.db.Exec(
		`INSERT INTO session (id, project_id, parent_id, title, directory, time_created, time_updated) VALUES (?, '', ?, ?, ?, ?, ?)`,
		id, parentID, title, dir, created, updated,
	)
	if err != nil {
		t.Fatalf("inserting subagent %s: %v", id, err)
	}
}

func insertMessage(t *testing.T, db *DB, id, sessionID string, created int64, data interface{}) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshalling message data: %v", err)
	}
	_, err = db.db.Exec(
		`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
		id, sessionID, created, string(raw),
	)
	if err != nil {
		t.Fatalf("inserting message %s: %v", id, err)
	}
}

func insertPart(t *testing.T, db *DB, id, messageID, sessionID string, created int64, data interface{}) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshalling part data: %v", err)
	}
	_, err = db.db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, data) VALUES (?, ?, ?, ?, ?)`,
		id, messageID, sessionID, created, string(raw),
	)
	if err != nil {
		t.Fatalf("inserting part %s: %v", id, err)
	}
}

// --- InferSessionStatus tests ---

func TestInferSessionStatus(t *testing.T) {
	tests := []struct {
		name                string
		role                string
		finish              string
		lastError           string
		synthesizedTerminal bool
		wantStatus          string
	}{
		{"no messages (empty role)", "", "", "", false, "done"},
		{"user message last", "user", "", "", false, "done"},
		{"assistant busy (no finish)", "assistant", "", "", false, "busy"},
		{"assistant waiting (finish set)", "assistant", "end_turn", "", false, "waiting"},
		{"assistant error (finish=error)", "assistant", "error", "", false, "error"},
		{"assistant error (lastError set)", "assistant", "", "something broke", false, "error"},
		{"assistant error (both set)", "assistant", "error", "also error", false, "error"},
		// Synthesized terminal: assistant envelope with no LLM turn (e.g.
		// POST /session/{id}/shell). Never receives a `finish`; should be
		// reported as "done", not "busy".
		{"assistant synth-terminal (shell only)", "assistant", "", "", true, "done"},
		// finish/error still take precedence over the synth flag.
		{"assistant synth-terminal but finished", "assistant", "stop", "", true, "waiting"},
		{"assistant synth-terminal but errored", "assistant", "", "boom", true, "error"},
		// User messages ignore the synth flag.
		{"user message synth-terminal flag ignored", "user", "", "", true, "done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InferSessionStatus(tt.role, tt.finish, tt.lastError, tt.synthesizedTerminal)
			if got != tt.wantStatus {
				t.Errorf("InferSessionStatus(%q, %q, %q, %v) = %q, want %q",
					tt.role, tt.finish, tt.lastError, tt.synthesizedTerminal, got, tt.wantStatus)
			}
		})
	}
}

// --- GetSessions tests ---

func TestGetSessions_Empty(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestGetSessions_ReturnsSessionsWithStatus(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Test Session", "/project/a", now-10000, now)
	insertMessage(t, db, "m1", "s1", now-5000, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "m2", "s1", now, map[string]interface{}{
		"role":   "assistant",
		"finish": "end_turn",
		"tokens": map[string]interface{}{"input": 100, "output": 50},
		"cost":   0.005,
	})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.ID != "s1" {
		t.Errorf("expected ID s1, got %s", s.ID)
	}
	if s.Status != "waiting" {
		t.Errorf("expected status 'waiting', got %q", s.Status)
	}
	if s.MessageCount != 1 {
		t.Errorf("expected 1 user message, got %d", s.MessageCount)
	}
	if s.TotalInputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", s.TotalInputTokens)
	}
	if s.TotalOutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", s.TotalOutputTokens)
	}
}

func TestGetSessions_FilterByDirectory(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session A", "/project/a", now, now)
	insertSession(t, db, "s2", "Session B", "/project/b", now, now)

	sessions, err := db.GetSessions("/project/a", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "s1" {
		t.Errorf("expected s1, got %s", sessions[0].ID)
	}
}

func TestGetSessions_FilterBySince(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Old Session", "/project", now-100000, now-100000)
	insertSession(t, db, "s2", "New Session", "/project", now, now)

	sessions, err := db.GetSessions("", now-50000)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "s2" {
		t.Errorf("expected s2, got %s", sessions[0].ID)
	}
}

// TestGetSessions_ExcludesCompletedSubagents verifies that a subagent
// (a session with a non-NULL parent_id) is hidden once it finishes, but
// surfaced — with its parent link populated — while it's still active.
// Top-level sessions are always returned regardless of status.
func TestGetSessions_ExcludesCompletedSubagents(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "parent", "Normal Session", "/project", now, now)

	// A completed subagent: no messages → InferSessionStatus == "done".
	insertSubagent(t, db, "child-done", "parent", "Task (code subagent)", "/project", now, now)

	// An active subagent: last message is an assistant turn with no
	// finish → status "busy", so it must be shown.
	insertSubagent(t, db, "child-busy", "parent", "Task (build subagent)", "/project", now, now)
	insertMessage(t, db, "m1", "child-busy", now, map[string]interface{}{"role": "assistant"})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}

	got := map[string]Session{}
	for _, s := range sessions {
		got[s.ID] = s
	}
	if _, ok := got["parent"]; !ok {
		t.Errorf("expected top-level session 'parent' to be returned")
	}
	if _, ok := got["child-done"]; ok {
		t.Errorf("completed subagent 'child-done' should be excluded")
	}
	active, ok := got["child-busy"]
	if !ok {
		t.Fatalf("active subagent 'child-busy' should be returned")
	}
	if active.ParentID != "parent" {
		t.Errorf("expected child-busy.ParentID == 'parent', got %q", active.ParentID)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions (parent + active subagent), got %d", len(sessions))
	}
}

func TestGetSessions_StatusBusy(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Busy Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{"role": "assistant"})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Status != "busy" {
		t.Errorf("expected status 'busy', got %q", sessions[0].Status)
	}
}

func TestGetSessions_StatusError(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Error Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role":   "assistant",
		"finish": "error",
	})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if sessions[0].Status != "error" {
		t.Errorf("expected status 'error', got %q", sessions[0].Status)
	}
}

func TestGetSessions_StatusDone(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Done Session", "/project", now, now)
	// Last message is from user → done
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{"role": "user"})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if sessions[0].Status != "done" {
		t.Errorf("expected status 'done', got %q", sessions[0].Status)
	}
}

// TestGetSessions_StatusShellOnlySynthTerminal exercises the
// "synthesized terminal" path: a user types `!cmd` in the composer,
// which OpenCode persists as an assistant message containing a single
// completed bash tool part and *no* `finish` field. Without the
// parts-aware status check, this would be classified as "busy"
// indefinitely (and the next user message would be tagged "Queued").
func TestGetSessions_StatusShellOnlySynthTerminal(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Shell Session", "/project", now, now)
	insertMessage(t, db, "m_user", "s1", now-1000, map[string]interface{}{"role": "user"})
	// Synthesised assistant envelope: no `finish`, no LLM step-start,
	// just one completed bash tool.
	insertMessage(t, db, "m_shell", "s1", now, map[string]interface{}{"role": "assistant"})
	insertPart(t, db, "p1", "m_shell", "s1", now, map[string]interface{}{
		"type": "tool",
		"tool": "bash",
		"state": map[string]interface{}{"status": "completed"},
	})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if sessions[0].Status != "done" {
		t.Errorf("expected status 'done' for shell-only synthesised message, got %q", sessions[0].Status)
	}
}

// TestGetSessions_StatusLLMMidFlight ensures the synth-terminal heuristic
// does NOT misclassify a real LLM turn that has started streaming. A
// `step-start` part means an LLM turn is genuinely in flight and the
// session is busy until `finish` lands.
func TestGetSessions_StatusLLMMidFlight(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Live LLM Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{"role": "assistant"})
	insertPart(t, db, "p1", "m1", "s1", now, map[string]interface{}{"type": "step-start"})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if sessions[0].Status != "busy" {
		t.Errorf("expected status 'busy' for mid-flight LLM (step-start present), got %q", sessions[0].Status)
	}
}

// TestGetSessions_StatusLLMRunningTool ensures a real LLM turn with an
// in-flight tool call is not misclassified as done either. The `running`
// state on any part keeps the session "busy".
func TestGetSessions_StatusLLMRunningTool(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Running Tool Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{"role": "assistant"})
	// A tool with state.status=running should keep the session busy
	// even if no step-start is present (defensive: the SQL doesn't
	// require both signals — either is enough to refuse the synth flag).
	insertPart(t, db, "p1", "m1", "s1", now, map[string]interface{}{
		"type": "tool",
		"tool": "bash",
		"state": map[string]interface{}{"status": "running"},
	})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if sessions[0].Status != "busy" {
		t.Errorf("expected status 'busy' for in-flight tool, got %q", sessions[0].Status)
	}
}

// TestGetSessions_StatusEmptyPartsBusy ensures an assistant message with
// zero parts (just-created envelope, parts haven't streamed yet) stays
// "busy" — we only flip to done when there is concrete evidence of
// non-LLM origin (≥1 part with no step-start, no running).
func TestGetSessions_StatusEmptyPartsBusy(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Empty Parts", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{"role": "assistant"})

	sessions, err := db.GetSessions("", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if sessions[0].Status != "busy" {
		t.Errorf("expected status 'busy' for assistant message with no parts, got %q", sessions[0].Status)
	}
}

// --- GetSession tests ---

func TestGetSession_Found(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "My Session", "/project", now-1000, now)

	s, err := db.GetSession("s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s.ID != "s1" {
		t.Errorf("expected ID s1, got %s", s.ID)
	}
	if s.Title != "My Session" {
		t.Errorf("expected title 'My Session', got %q", s.Title)
	}
	if s.DurationMs != 1000 {
		t.Errorf("expected DurationMs=1000, got %d", s.DurationMs)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	_, err := db.GetSession("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

// --- GetSessionMessages tests ---

func TestGetSessionMessages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now-2000, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "m2", "s1", now-1000, map[string]interface{}{"role": "assistant"})
	insertMessage(t, db, "m3", "s2", now, map[string]interface{}{"role": "user"}) // different session

	messages, err := db.GetSessionMessages("s1")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].ID != "m1" || messages[1].ID != "m2" {
		t.Errorf("messages out of order: %s, %s", messages[0].ID, messages[1].ID)
	}
}

// --- GetSessionParts tests ---

func TestGetSessionParts(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{"role": "user"})
	insertPart(t, db, "p1", "m1", "s1", now, map[string]interface{}{"type": "text", "text": "hello"})
	insertPart(t, db, "p2", "m1", "s1", now+1, map[string]interface{}{"type": "text", "text": "world"})
	insertPart(t, db, "p3", "m1", "s2", now+2, map[string]interface{}{"type": "text"}) // different session

	parts, err := db.GetSessionParts("s1")
	if err != nil {
		t.Fatalf("GetSessionParts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
}

// --- PaginateMessages tests ---

func TestPaginateMessages(t *testing.T) {
	msgs := []Message{
		{ID: "m1"}, {ID: "m2"}, {ID: "m3"}, {ID: "m4"}, {ID: "m5"},
	}

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantIDs   []string
		wantTotal int
	}{
		{"last 2", 2, 0, []string{"m4", "m5"}, 5},
		{"last 2, offset 1", 2, 1, []string{"m3", "m4"}, 5},
		{"all", 10, 0, []string{"m1", "m2", "m3", "m4", "m5"}, 5},
		{"offset beyond", 2, 10, nil, 5},
		{"empty input", 5, 0, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := msgs
			if tt.name == "empty input" {
				input = nil
			}
			result, total := PaginateMessages(input, tt.limit, tt.offset)
			if total != tt.wantTotal {
				t.Errorf("total = %d, want %d", total, tt.wantTotal)
			}
			if len(result) != len(tt.wantIDs) {
				t.Fatalf("got %d results, want %d", len(result), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if result[i].ID != id {
					t.Errorf("result[%d].ID = %q, want %q", i, result[i].ID, id)
				}
			}
		})
	}
}

// --- FilterPartsForMessages tests ---

func TestFilterPartsForMessages(t *testing.T) {
	parts := []Part{
		{ID: "p1", MessageID: "m1"},
		{ID: "p2", MessageID: "m2"},
		{ID: "p3", MessageID: "m1"},
		{ID: "p4", MessageID: "m3"},
	}
	messages := []Message{{ID: "m1"}, {ID: "m3"}}

	result := FilterPartsForMessages(parts, messages)
	if len(result) != 3 {
		t.Fatalf("expected 3 filtered parts, got %d", len(result))
	}
	expectedIDs := map[string]bool{"p1": true, "p3": true, "p4": true}
	for _, p := range result {
		if !expectedIDs[p.ID] {
			t.Errorf("unexpected part %s", p.ID)
		}
	}
}

func TestFilterPartsForMessages_Empty(t *testing.T) {
	result := FilterPartsForMessages(nil, nil)
	if len(result) != 0 {
		t.Errorf("expected 0 parts, got %d", len(result))
	}
}

// --- GetSessionsInactiveBefore tests ---

func TestGetSessionsInactiveBefore(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Old", "/project", now-200000, now-200000)
	insertSession(t, db, "s2", "Recent", "/project", now, now)
	insertSession(t, db, "s3", "Subagent (code subagent)", "/project", now-200000, now-200000)

	cutoff := now - 100000
	candidates, err := db.GetSessionsInactiveBefore(cutoff)
	if err != nil {
		t.Fatalf("GetSessionsInactiveBefore: %v", err)
	}
	// s1 should be returned, s2 is too recent, s3 is a subagent (excluded)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].ID != "s1" {
		t.Errorf("expected s1, got %s", candidates[0].ID)
	}
}

// --- GetSessionDefaults tests ---

func TestGetSessionDefaults_NoMessages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	defaults, err := db.GetSessionDefaults("s1", "/project")
	if err != nil {
		t.Fatalf("GetSessionDefaults: %v", err)
	}
	if defaults.Agent != "" || defaults.Model != "" {
		t.Errorf("expected empty defaults, got agent=%q model=%q", defaults.Agent, defaults.Model)
	}
}

func TestGetSessionDefaults_WithMessages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Current", "/project", now, now)
	insertSession(t, db, "s2", "Previous", "/project", now-10000, now-10000)

	// Insert an assistant message in s2 with agent and model
	insertMessage(t, db, "m1", "s2", now-5000, map[string]interface{}{
		"role":       "assistant",
		"agent":      "code",
		"providerID": "anthropic",
		"modelID":    "claude-3-5-sonnet",
	})

	defaults, err := db.GetSessionDefaults("s1", "/project")
	if err != nil {
		t.Fatalf("GetSessionDefaults: %v", err)
	}
	if defaults.Agent != "code" {
		t.Errorf("expected agent 'code', got %q", defaults.Agent)
	}
	if defaults.Model != "anthropic/claude-3-5-sonnet" {
		t.Errorf("expected model 'anthropic/claude-3-5-sonnet', got %q", defaults.Model)
	}
}

// --- GetContextTokenCount tests ---

func TestGetContextTokenCount_NoMessages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	count, err := db.GetContextTokenCount("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestGetContextTokenCount_WithTokens(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role": "assistant",
		"tokens": map[string]interface{}{
			"input":     100,
			"output":    50,
			"reasoning": 10,
			"cache":     map[string]interface{}{"read": 5, "write": 3},
		},
	})

	count, err := db.GetContextTokenCount("s1")
	if err != nil {
		t.Fatalf("GetContextTokenCount: %v", err)
	}
	expected := int64(100 + 50 + 10 + 5 + 3)
	if count != expected {
		t.Errorf("expected %d, got %d", expected, count)
	}
}

// --- Stats tests ---

func TestGetStats(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session 1", "/a", now, now)
	insertSession(t, db, "s2", "Session 2", "/b", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "m2", "s1", now, map[string]interface{}{
		"role":   "assistant",
		"tokens": map[string]interface{}{"input": 100, "output": 50},
		"cost":   0.01,
	})

	stats, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalSessions != 2 {
		t.Errorf("TotalSessions = %d, want 2", stats.TotalSessions)
	}
	if stats.TotalMessages != 1 {
		t.Errorf("TotalMessages = %d, want 1", stats.TotalMessages)
	}
	if stats.TotalProjects != 2 {
		t.Errorf("TotalProjects = %d, want 2", stats.TotalProjects)
	}
	if stats.TotalTokensIn != 100 {
		t.Errorf("TotalTokensIn = %d, want 100", stats.TotalTokensIn)
	}
	if stats.TotalTokensOut != 50 {
		t.Errorf("TotalTokensOut = %d, want 50", stats.TotalTokensOut)
	}
}

func TestGetMetricsDashboard(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session 1", "/a", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role":       "assistant",
		"agent":      "build",
		"providerID": "anthropic",
		"modelID":    "opus-4.1",
		"finish":     "end_turn",
		"cost":       0.25,
		"time":       map[string]interface{}{"created": now - 4000, "completed": now},
		"tokens": map[string]interface{}{
			"input":  100,
			"output": 200,
			"cache":  map[string]interface{}{"read": 300, "write": 100},
		},
	})

	metrics, err := db.GetMetricsDashboard(MetricsDashboardOptions{
		RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})
	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if metrics.Summary.Requests != 1 {
		t.Fatalf("Requests = %d, want 1", metrics.Summary.Requests)
	}
	if metrics.Summary.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", metrics.Summary.TotalTokens)
	}
	if metrics.Summary.CacheHitRate != 0.75 {
		t.Errorf("CacheHitRate = %v, want 0.75", metrics.Summary.CacheHitRate)
	}
	if len(metrics.AvailableAgents) != 1 || metrics.AvailableAgents[0] != "build" {
		t.Errorf("AvailableAgents = %#v, want [build]", metrics.AvailableAgents)
	}
	if len(metrics.AvailableModels) != 1 || metrics.AvailableModels[0] != "anthropic/opus-4.1" {
		t.Errorf("AvailableModels = %#v, want [anthropic/opus-4.1]", metrics.AvailableModels)
	}
	if len(metrics.StopReasons) != 1 || metrics.StopReasons[0].Reason != "end_turn" {
		t.Errorf("StopReasons = %#v, want end_turn", metrics.StopReasons)
	}
	if len(metrics.Requests) != 1 || metrics.Requests[0].TokensPerSecond != 50 {
		t.Errorf("Requests = %#v, want tokensPerSecond=50", metrics.Requests)
	}
	if metrics.TotalSessions != 1 {
		t.Errorf("TotalSessions = %d, want 1", metrics.TotalSessions)
	}
	if len(metrics.Sessions) != 1 {
		t.Fatalf("Sessions len = %d, want 1", len(metrics.Sessions))
	}
	sess := metrics.Sessions[0]
	if sess.ID != "s1" {
		t.Errorf("Sessions[0].ID = %q, want s1", sess.ID)
	}
	if sess.Title != "Session 1" {
		t.Errorf("Sessions[0].Title = %q, want Session 1", sess.Title)
	}
	if sess.Directory != "/a" {
		t.Errorf("Sessions[0].Directory = %q, want /a", sess.Directory)
	}
	if sess.Requests != 1 {
		t.Errorf("Sessions[0].Requests = %d, want 1", sess.Requests)
	}
	if sess.TotalTokens != 300 {
		t.Errorf("Sessions[0].TotalTokens = %d, want 300", sess.TotalTokens)
	}
	if sess.Cost != 0.25 {
		t.Errorf("Sessions[0].Cost = %v, want 0.25", sess.Cost)
	}
	if sess.AvgTokensPerSec != 50 {
		t.Errorf("Sessions[0].AvgTokensPerSec = %v, want 50", sess.AvgTokensPerSec)
	}
	if len(sess.Agents) != 1 || sess.Agents[0] != "build" {
		t.Errorf("Sessions[0].Agents = %#v, want [build]", sess.Agents)
	}
	if len(sess.Models) != 1 || sess.Models[0] != "anthropic/opus-4.1" {
		t.Errorf("Sessions[0].Models = %#v, want [anthropic/opus-4.1]", sess.Models)
	}
	if sess.ErrorCount != 0 {
		t.Errorf("Sessions[0].ErrorCount = %d, want 0", sess.ErrorCount)
	}
}

func TestGetMetricsDashboardSessionAggregation(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	// Two sessions; s1 gets 2 requests, s2 gets 1. s2 is more recent.
	insertSession(t, db, "s1", "Session One", "/proj/a", now-10000, now-1000)
	insertSession(t, db, "s2", "Session Two", "/proj/b", now-500, now)

	insertMessage(t, db, "m1", "s1", now-8000, map[string]interface{}{
		"role":       "assistant",
		"agent":      "build",
		"providerID": "anthropic",
		"modelID":    "opus-4.1",
		"finish":     "end_turn",
		"cost":       0.10,
		"time":       map[string]interface{}{"created": now - 9000, "completed": now - 7000},
		"tokens":     map[string]interface{}{"input": 50, "output": 100, "cache": map[string]interface{}{"read": 10, "write": 5}},
	})
	insertMessage(t, db, "m2", "s1", now-2000, map[string]interface{}{
		"role":       "assistant",
		"agent":      "review",
		"providerID": "anthropic",
		"modelID":    "opus-4.1",
		"finish":     "error",
		"cost":       0.05,
		"time":       map[string]interface{}{"created": now - 3000, "completed": now - 2500},
		"tokens":     map[string]interface{}{"input": 20, "output": 0},
	})
	insertMessage(t, db, "m3", "s2", now, map[string]interface{}{
		"role":       "assistant",
		"agent":      "build",
		"providerID": "openai",
		"modelID":    "gpt-5",
		"finish":     "end_turn",
		"cost":       0.01,
		"time":       map[string]interface{}{"created": now - 500, "completed": now},
		"tokens":     map[string]interface{}{"input": 10, "output": 20},
	})

	metrics, err := db.GetMetricsDashboard(MetricsDashboardOptions{
		RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})
	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}

	if metrics.TotalSessions != 2 {
		t.Fatalf("TotalSessions = %d, want 2", metrics.TotalSessions)
	}
	if len(metrics.Sessions) != 2 {
		t.Fatalf("Sessions len = %d, want 2", len(metrics.Sessions))
	}
	// Cost descending — s1 (Cost=0.15) outranks s2 (Cost=0.01).
	if metrics.Sessions[0].ID != "s1" {
		t.Errorf("Sessions[0].ID = %q, want s1 (highest cost)", metrics.Sessions[0].ID)
	}
	if metrics.Sessions[1].ID != "s2" {
		t.Errorf("Sessions[1].ID = %q, want s2", metrics.Sessions[1].ID)
	}

	// s1 aggregation.
	s1 := metrics.Sessions[0]
	if s1.Requests != 2 {
		t.Errorf("s1.Requests = %d, want 2", s1.Requests)
	}
	if s1.InputTokens != 70 || s1.OutputTokens != 100 {
		t.Errorf("s1 tokens = in=%d out=%d, want in=70 out=100", s1.InputTokens, s1.OutputTokens)
	}
	if diff := s1.Cost - 0.15; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("s1.Cost = %v, want ~0.15", s1.Cost)
	}
	if s1.ErrorCount != 1 {
		t.Errorf("s1.ErrorCount = %d, want 1", s1.ErrorCount)
	}
	if len(s1.Agents) != 2 {
		t.Errorf("s1.Agents = %#v, want 2 entries", s1.Agents)
	}

	// Pagination: sessionLimit=1 returns just the most expensive.
	paged, err := db.GetMetricsDashboard(MetricsDashboardOptions{
		RequestLimit: 50, SessionLimit: 1, ProjectLimit: 50,
	})
	if err != nil {
		t.Fatalf("GetMetricsDashboard paged: %v", err)
	}
	if len(paged.Sessions) != 1 || paged.Sessions[0].ID != "s1" {
		t.Errorf("paged sessions = %#v, want [s1] (highest cost)", paged.Sessions)
	}
	if paged.TotalSessions != 2 {
		t.Errorf("paged.TotalSessions = %d, want 2", paged.TotalSessions)
	}

	// Offset skips the most expensive (s1) — what's left is s2.
	offset, err := db.GetMetricsDashboard(MetricsDashboardOptions{
		RequestLimit: 50, SessionLimit: 1, SessionOffset: 1, ProjectLimit: 50,
	})
	if err != nil {
		t.Fatalf("GetMetricsDashboard offset: %v", err)
	}
	if len(offset.Sessions) != 1 || offset.Sessions[0].ID != "s2" {
		t.Errorf("offset sessions = %#v, want [s2]", offset.Sessions)
	}
}

func TestGetProjects(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session 1", "/project/a", now, now)
	insertSession(t, db, "s2", "Session 2", "/project/a", now, now)
	insertSession(t, db, "s3", "Session 3", "/project/b", now, now)
	// Subagent session under /project/a — must be excluded from
	// SessionCount but its token/cost contributions must still
	// be folded into the project totals (real spend).
	insertSession(t, db, "s4", "Task (code subagent)", "/project/a", now, now)
	insertMessage(t, db, "m1", "s4", now, map[string]interface{}{
		"role":   "assistant",
		"tokens": map[string]interface{}{"input": 100, "output": 50},
		"cost":   0.25,
	})

	projects, err := db.GetProjects()
	if err != nil {
		t.Fatalf("GetProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	var foundA bool
	for _, p := range projects {
		if p.Directory != "/project/a" {
			continue
		}
		foundA = true
		if p.SessionCount != 2 {
			t.Errorf("project /a: expected 2 sessions (subagent excluded), got %d", p.SessionCount)
		}
		if p.TotalTokensIn != 100 {
			t.Errorf("project /a: expected 100 input tokens (subagent included), got %d", p.TotalTokensIn)
		}
		if p.TotalTokensOut != 50 {
			t.Errorf("project /a: expected 50 output tokens (subagent included), got %d", p.TotalTokensOut)
		}
		if p.TotalCost != 0.25 {
			t.Errorf("project /a: expected cost 0.25 (subagent included), got %v", p.TotalCost)
		}
	}
	if !foundA {
		t.Fatalf("project /a not returned")
	}
}

func TestGetHourlyActivity(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	result, err := db.GetHourlyActivity(0, "")
	if err != nil {
		t.Fatalf("GetHourlyActivity: %v", err)
	}
	if len(result) != 24 {
		t.Errorf("expected 24 hours, got %d", len(result))
	}
	for i, h := range result {
		if h.Hour != i {
			t.Errorf("hour %d has Hour=%d", i, h.Hour)
		}
	}
}

func TestGetModelUsage(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role":       "assistant",
		"providerID": "anthropic",
		"modelID":    "claude-3",
		"tokens":     map[string]interface{}{"input": 100, "output": 50},
	})
	insertMessage(t, db, "m2", "s1", now, map[string]interface{}{
		"role":       "assistant",
		"providerID": "anthropic",
		"modelID":    "claude-3",
		"tokens":     map[string]interface{}{"input": 200, "output": 100},
	})

	result, err := db.GetModelUsage(0, "")
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result))
	}
	if result[0].Provider != "anthropic" || result[0].Model != "claude-3" {
		t.Errorf("unexpected model: %s/%s", result[0].Provider, result[0].Model)
	}
	if result[0].Count != 2 {
		t.Errorf("expected count=2, got %d", result[0].Count)
	}
	if result[0].TokensIn != 300 || result[0].TokensOut != 150 {
		t.Errorf("expected tokens 300/150, got %d/%d", result[0].TokensIn, result[0].TokensOut)
	}
	if result[0].CacheRead != 0 || result[0].CacheWrite != 0 {
		t.Errorf("expected cache 0/0 (no cache data), got %d/%d", result[0].CacheRead, result[0].CacheWrite)
	}
}

func TestGetModelUsage_CacheTokens(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	// Message with cache tokens (Anthropic-style prompt caching).
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role":       "assistant",
		"providerID": "anthropic",
		"modelID":    "claude-3-5-sonnet",
		"tokens": map[string]interface{}{
			"input":  500,
			"output": 200,
			"cache":  map[string]interface{}{"read": 1000, "write": 300},
		},
	})
	// Second message: only cache read, no write.
	insertMessage(t, db, "m2", "s1", now, map[string]interface{}{
		"role":       "assistant",
		"providerID": "anthropic",
		"modelID":    "claude-3-5-sonnet",
		"tokens": map[string]interface{}{
			"input":  400,
			"output": 150,
			"cache":  map[string]interface{}{"read": 800, "write": 0},
		},
	})

	result, err := db.GetModelUsage(0, "")
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result))
	}
	r := result[0]
	if r.TokensIn != 900 || r.TokensOut != 350 {
		t.Errorf("tokensIn/Out = %d/%d, want 900/350", r.TokensIn, r.TokensOut)
	}
	if r.CacheRead != 1800 || r.CacheWrite != 300 {
		t.Errorf("cacheRead/Write = %d/%d, want 1800/300", r.CacheRead, r.CacheWrite)
	}
}

func TestGetModelUsage_SortedOutput(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role": "assistant", "providerID": "openai", "modelID": "gpt-4",
		"tokens": map[string]interface{}{"input": 10, "output": 5},
	})
	insertMessage(t, db, "m2", "s1", now, map[string]interface{}{
		"role": "assistant", "providerID": "anthropic", "modelID": "claude-3",
		"tokens": map[string]interface{}{"input": 20, "output": 10},
	})

	result, err := db.GetModelUsage(0, "")
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result))
	}
	// Should be sorted: anthropic before openai
	if result[0].Provider != "anthropic" {
		t.Errorf("expected first provider 'anthropic', got %q", result[0].Provider)
	}
	if result[1].Provider != "openai" {
		t.Errorf("expected second provider 'openai', got %q", result[1].Provider)
	}
}

func TestGetModelUsage_FallbackToNestedModel(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role": "assistant",
		"model": map[string]interface{}{
			"providerID": "google",
			"modelID":    "gemini-pro",
		},
		"tokens": map[string]interface{}{"input": 50, "output": 25},
	})

	result, err := db.GetModelUsage(0, "")
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result))
	}
	if result[0].Provider != "google" || result[0].Model != "gemini-pro" {
		t.Errorf("expected google/gemini-pro, got %s/%s", result[0].Provider, result[0].Model)
	}
}

func TestGetDailyActivity(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	result, err := db.GetDailyActivity(0, "", "")
	if err != nil {
		t.Fatalf("GetDailyActivity: %v", err)
	}
	// Should return 366 entries (365 days + today)
	if len(result) != 366 {
		t.Errorf("expected 366 daily entries, got %d", len(result))
	}
}

func TestGetDailyActivity_UserMessages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)

	// Insert 2 user messages and 3 assistant messages today.
	insertMessage(t, db, "u1", "s1", now, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "u2", "s1", now, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "a1", "s1", now, map[string]interface{}{
		"role": "assistant", "providerID": "anthropic", "modelID": "claude",
		"tokens": map[string]interface{}{"input": 10, "output": 5},
	})
	insertMessage(t, db, "a2", "s1", now, map[string]interface{}{
		"role": "assistant", "providerID": "anthropic", "modelID": "claude",
		"tokens": map[string]interface{}{"input": 10, "output": 5},
	})
	insertMessage(t, db, "a3", "s1", now, map[string]interface{}{
		"role": "assistant", "providerID": "anthropic", "modelID": "claude",
		"tokens": map[string]interface{}{"input": 10, "output": 5},
	})

	result, err := db.GetDailyActivity(0, "", "")
	if err != nil {
		t.Fatalf("GetDailyActivity: %v", err)
	}

	// Find today's entry and verify counts.
	today := time.Now().Format("2006-01-02")
	var found bool
	for _, d := range result {
		if d.Date == today {
			found = true
			if d.Messages != 3 {
				t.Errorf("Messages = %d, want 3 (assistant messages)", d.Messages)
			}
			if d.UserMessages != 2 {
				t.Errorf("UserMessages = %d, want 2", d.UserMessages)
			}
			break
		}
	}
	if !found {
		t.Fatal("today's entry not found in result")
	}
}

// --- GetNewAssistantMessages tests ---

func TestGetNewAssistantMessages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)

	// Insert messages at known timestamps.
	insertMessage(t, db, "u1", "s1", now-3000, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "a1", "s1", now-2000, map[string]interface{}{
		"role": "assistant", "providerID": "anthropic", "modelID": "claude-3",
		"tokens": map[string]interface{}{"input": 100, "output": 50, "cache": map[string]interface{}{"read": 80, "write": 20}},
		"cost":   0.005,
		"finish": "end_turn",
		"time":   map[string]interface{}{"created": now - 3000, "completed": now - 2000},
	})
	insertMessage(t, db, "a2", "s1", now-1000, map[string]interface{}{
		"role": "assistant", "providerID": "google", "modelID": "gemini",
		"tokens": map[string]interface{}{"input": 200, "output": 100},
		"cost":   0.01,
		"finish": "error",
		"time":   map[string]interface{}{"created": now - 2000, "completed": now - 1000},
	})

	// Query all messages since before the first one.
	rows, hwm, err := db.GetNewAssistantMessages(now - 5000)
	if err != nil {
		t.Fatalf("GetNewAssistantMessages: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if hwm != now-1000 {
		t.Errorf("high-water mark = %d, want %d", hwm, now-1000)
	}

	// First row should be a1 (oldest first).
	r := rows[0]
	if r.Model != "anthropic/claude-3" {
		t.Errorf("row[0].Model = %q, want %q", r.Model, "anthropic/claude-3")
	}
	if r.InputTokens != 100 || r.OutputTokens != 50 {
		t.Errorf("row[0] tokens = %d/%d, want 100/50", r.InputTokens, r.OutputTokens)
	}
	if r.CacheReadTokens != 80 || r.CacheWriteTokens != 20 {
		t.Errorf("row[0] cache = %d/%d, want 80/20", r.CacheReadTokens, r.CacheWriteTokens)
	}
	if r.Cost != 0.005 {
		t.Errorf("row[0].Cost = %f, want 0.005", r.Cost)
	}
	if r.StopReason != "end_turn" {
		t.Errorf("row[0].StopReason = %q, want %q", r.StopReason, "end_turn")
	}
	if r.DurationMs != 1000 {
		t.Errorf("row[0].DurationMs = %d, want 1000", r.DurationMs)
	}

	// Second query with the returned high-water mark should return nothing.
	rows2, hwm2, err := db.GetNewAssistantMessages(hwm)
	if err != nil {
		t.Fatalf("GetNewAssistantMessages (second call): %v", err)
	}
	if len(rows2) != 0 {
		t.Errorf("expected 0 rows on second call, got %d", len(rows2))
	}
	if hwm2 != hwm {
		t.Errorf("high-water mark changed: %d -> %d", hwm, hwm2)
	}
}

func TestGetNewAssistantMessages_Empty(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	rows, hwm, err := db.GetNewAssistantMessages(0)
	if err != nil {
		t.Fatalf("GetNewAssistantMessages: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
	if hwm != 0 {
		t.Errorf("high-water mark = %d, want 0", hwm)
	}
}

// --- extractModelProvider tests ---

func TestExtractModelProvider(t *testing.T) {
	tests := []struct {
		name         string
		data         MessageData
		wantProvider string
		wantModel    string
	}{
		{
			"top-level fields",
			MessageData{ProviderID: "anthropic", ModelID: "claude-3"},
			"anthropic", "claude-3",
		},
		{
			"nested model",
			MessageData{Model: &ModelRef{ProviderID: "google", ModelID: "gemini"}},
			"google", "gemini",
		},
		{
			"top-level takes precedence",
			MessageData{
				ProviderID: "anthropic", ModelID: "claude",
				Model: &ModelRef{ProviderID: "google", ModelID: "gemini"},
			},
			"anthropic", "claude",
		},
		{
			"empty data",
			MessageData{},
			"", "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model := extractModelProvider(tt.data)
			if provider != tt.wantProvider || model != tt.wantModel {
				t.Errorf("extractModelProvider() = (%q, %q), want (%q, %q)",
					provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

// --- GetHourlyTokensByModel tests ---

func TestGetHourlyTokensByModel(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session", "/project", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role":       "assistant",
		"providerID": "anthropic",
		"modelID":    "claude-3",
		"tokens":     map[string]interface{}{"input": 100, "output": 50},
	})
	insertMessage(t, db, "m2", "s1", now, map[string]interface{}{
		"role":       "assistant",
		"providerID": "anthropic",
		"modelID":    "claude-3",
		"tokens":     map[string]interface{}{"input": 200, "output": 100},
	})

	result, err := db.GetHourlyTokensByModel(7, 0, "", "")
	if err != nil {
		t.Fatalf("GetHourlyTokensByModel: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 hourly entry, got %d", len(result))
	}
	if result[0].Provider != "anthropic" || result[0].Model != "claude-3" {
		t.Errorf("unexpected model: %s/%s", result[0].Provider, result[0].Model)
	}
	if result[0].TokensIn != 300 || result[0].TokensOut != 150 {
		t.Errorf("expected tokens 300/150, got %d/%d", result[0].TokensIn, result[0].TokensOut)
	}
}

func TestGetHourlyTokensByModel_Empty(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	result, err := db.GetHourlyTokensByModel(7, 0, "", "")
	if err != nil {
		t.Fatalf("GetHourlyTokensByModel: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 entries, got %d", len(result))
	}
}

// --- GetSubagentSessionIDs tests ---

// TestGetSubagentSessionIDs_ReturnsChildren verifies that the DB returns
// every session whose parent_id matches the given parent. This powers
// the bubble-up of subagent permission/question prompts to the parent
// session in the UI.
func TestGetSubagentSessionIDs_ReturnsChildren(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "parent", "Parent", "/project", now, now)
	insertSubagent(t, db, "child1", "parent", "Task (explore subagent)", "/project", now, now)
	insertSubagent(t, db, "child2", "parent", "Task (build subagent)", "/project", now, now)
	// A subagent of an unrelated parent — must NOT appear in the result.
	insertSession(t, db, "other", "Other parent", "/project", now, now)
	insertSubagent(t, db, "child3", "other", "Task (build subagent)", "/project", now, now)

	got, err := db.GetSubagentSessionIDs("parent")
	if err != nil {
		t.Fatalf("GetSubagentSessionIDs: %v", err)
	}
	want := map[string]bool{"child1": true, "child2": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d ids, got %d (%v)", len(want), len(got), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected id in result: %q", id)
		}
	}
}

// TestGetSubagentSessionIDs_NoChildren returns an empty slice (and nil
// error) when the session has no subagents — most sessions don't.
func TestGetSubagentSessionIDs_NoChildren(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "lonely", "Lonely", "/project", now, now)

	got, err := db.GetSubagentSessionIDs("lonely")
	if err != nil {
		t.Fatalf("GetSubagentSessionIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no children, got %v", got)
	}
}

// TestGetSubagentSessionIDs_EmptyParentID short-circuits an empty
// argument — never returns every subagent in the DB.
func TestGetSubagentSessionIDs_EmptyParentID(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "parent", "Parent", "/project", now, now)
	insertSubagent(t, db, "child1", "parent", "Task (explore subagent)", "/project", now, now)

	got, err := db.GetSubagentSessionIDs("")
	if err != nil {
		t.Fatalf("GetSubagentSessionIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no children for empty parent id, got %v", got)
	}
}

// --- GetSessionParentIDs tests ---

// TestGetSessionParentIDs_ReturnsMap returns child→parent for every
// id that has a non-NULL parent_id. Top-level sessions are absent
// from the map (their parent_id is NULL).
func TestGetSessionParentIDs_ReturnsMap(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "parent", "Parent", "/project", now, now)
	insertSubagent(t, db, "child1", "parent", "Task (explore subagent)", "/project", now, now)
	insertSubagent(t, db, "child2", "parent", "Task (build subagent)", "/project", now, now)
	insertSession(t, db, "lonely", "Lonely", "/project", now, now)
	// Also seed an unrelated session that shouldn't appear in the result.
	insertSession(t, db, "unrelated", "Unrelated", "/project", now, now)

	got, err := db.GetSessionParentIDs([]string{"child1", "child2", "lonely", "missing"})
	if err != nil {
		t.Fatalf("GetSessionParentIDs: %v", err)
	}
	if got["child1"] != "parent" {
		t.Errorf("child1 parent = %q, want parent", got["child1"])
	}
	if got["child2"] != "parent" {
		t.Errorf("child2 parent = %q, want parent", got["child2"])
	}
	if _, ok := got["lonely"]; ok {
		t.Errorf("lonely should not be in the map (no parent_id), got %q", got["lonely"])
	}
	if _, ok := got["missing"]; ok {
		t.Errorf("missing id should not be in the map, got %q", got["missing"])
	}
	if _, ok := got["unrelated"]; ok {
		t.Errorf("unrelated id was not requested but appeared in the map")
	}
}

// stubPricing is a CostCalculator that returns a fixed per-token cost.
type stubPricing struct {
	inputRate  float64
	outputRate float64
}

func (s stubPricing) CalcCost(_ string, in, out, _, _ int64) float64 {
	return float64(in)*s.inputRate + float64(out)*s.outputRate
}

func TestGetMetricsDashboardCumulativeCalcCost(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "Session 1", "/a", now-10000, now)

	// Two messages with known token counts and reported cost.
	insertMessage(t, db, "m1", "s1", now-5000, map[string]interface{}{
		"role":       "assistant",
		"agent":      "build",
		"providerID": "anthropic",
		"modelID":    "opus-4.1",
		"finish":     "end_turn",
		"cost":       0.25,
		"time":       map[string]interface{}{"created": float64(now - 7000), "completed": float64(now - 5000)},
		"tokens": map[string]interface{}{
			"input":  100,
			"output": 200,
			"cache":  map[string]interface{}{"read": 0, "write": 0},
		},
	})
	insertMessage(t, db, "m2", "s1", now-1000, map[string]interface{}{
		"role":       "assistant",
		"agent":      "build",
		"providerID": "anthropic",
		"modelID":    "opus-4.1",
		"finish":     "end_turn",
		"cost":       0.75,
		"time":       map[string]interface{}{"created": float64(now - 3000), "completed": float64(now - 1000)},
		"tokens": map[string]interface{}{
			"input":  200,
			"output": 400,
			"cache":  map[string]interface{}{"read": 0, "write": 0},
		},
	})

	// inputRate=0.01, outputRate=0.02 → m1 calc = 100*0.01 + 200*0.02 = 5.0
	//                                   m2 calc = 200*0.01 + 400*0.02 = 10.0
	pricing := stubPricing{inputRate: 0.01, outputRate: 0.02}

	metrics, err := db.GetMetricsDashboard(MetricsDashboardOptions{
		RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
		Pricing: pricing,
	})
	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}

	// Summary should have both cost types.
	if metrics.Summary.TotalCost != 1.0 {
		t.Errorf("Summary.TotalCost = %v, want 1.0", metrics.Summary.TotalCost)
	}
	if metrics.Summary.TotalCalcCost != 15.0 {
		t.Errorf("Summary.TotalCalcCost = %v, want 15.0", metrics.Summary.TotalCalcCost)
	}

	// The last series point should have the full cumulative values.
	if len(metrics.Series) == 0 {
		t.Fatal("Series is empty")
	}
	last := metrics.Series[len(metrics.Series)-1]
	if last.CumulativeCost != 1.0 {
		t.Errorf("last Series.CumulativeCost = %v, want 1.0", last.CumulativeCost)
	}
	if last.CumulativeCalcCost != 15.0 {
		t.Errorf("last Series.CumulativeCalcCost = %v, want 15.0", last.CumulativeCalcCost)
	}
}

// TestGetSessionParentIDs_Empty short-circuits when no IDs are requested.
func TestGetSessionParentIDs_Empty(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	got, err := db.GetSessionParentIDs(nil)
	if err != nil {
		t.Fatalf("GetSessionParentIDs(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for nil input, got %v", got)
	}
}

// --- MessageCountsSince tests ---

func TestMessageCountsSince_EmptyInput(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	got, err := db.MessageCountsSince(nil)
	if err != nil {
		t.Fatalf("MessageCountsSince(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for nil input, got %v", got)
	}
}

func TestMessageCountsSince_Basic(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	// s1 has 3 messages at t=100, 200, 300.
	// s2 has 2 messages at t=150, 250.
	// s3 has no messages.
	insertSession(t, db, "s1", "S1", "/d", 100, 300)
	insertSession(t, db, "s2", "S2", "/d", 150, 250)
	insertSession(t, db, "s3", "S3", "/d", 100, 100)
	insertMessage(t, db, "s1-m1", "s1", 100, nil)
	insertMessage(t, db, "s1-m2", "s1", 200, nil)
	insertMessage(t, db, "s1-m3", "s1", 300, nil)
	insertMessage(t, db, "s2-m1", "s2", 150, nil)
	insertMessage(t, db, "s2-m2", "s2", 250, nil)

	tests := []struct {
		name    string
		cutoffs map[string]int64
		want    map[string]int
	}{
		{
			name:    "cutoff zero counts everything",
			cutoffs: map[string]int64{"s1": 0, "s2": 0, "s3": 0},
			want:    map[string]int{"s1": 3, "s2": 2}, // s3 omitted (zero)
		},
		{
			name:    "partial cutoff",
			cutoffs: map[string]int64{"s1": 150, "s2": 200},
			want:    map[string]int{"s1": 2, "s2": 1},
		},
		{
			name:    "cutoff at or past last message",
			cutoffs: map[string]int64{"s1": 300, "s2": 250},
			want:    map[string]int{}, // both fully read, omitted
		},
		{
			name:    "unknown session id returns no row",
			cutoffs: map[string]int64{"nope": 0},
			want:    map[string]int{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.MessageCountsSince(tc.cutoffs)
			if err != nil {
				t.Fatalf("MessageCountsSince: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len: want %d, got %d (%v)", len(tc.want), len(got), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s: want %d, got %d", k, v, got[k])
				}
			}
		})
	}
}

func TestMessageCountsSince_StrictlyGreater(t *testing.T) {
	// The cutoff uses strict > so a session that has exactly one
	// message at time_created == cutoff reports zero unread.
	db := openTestDB(t)
	defer db.Close()

	insertSession(t, db, "s1", "S1", "/d", 100, 100)
	insertMessage(t, db, "m1", "s1", 100, nil)

	got, err := db.MessageCountsSince(map[string]int64{"s1": 100})
	if err != nil {
		t.Fatalf("MessageCountsSince: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no unread when cutoff equals last message time, got %v", got)
	}
}
