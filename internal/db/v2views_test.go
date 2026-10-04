package db

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// v2SchemaDDL is a minimal OpenCode v2 schema: the session_v2 columns the
// session view selects, plus session_message.
const v2SchemaDDL = `
CREATE TABLE session_v2 (
	id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '', workspace_id TEXT, parent_id TEXT,
	slug TEXT, directory TEXT NOT NULL DEFAULT '', path TEXT, title TEXT, version TEXT,
	share_url TEXT, summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER,
	summary_diffs TEXT, metadata TEXT, cost REAL, tokens_input INTEGER, tokens_output INTEGER,
	tokens_reasoning INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER,
	revert TEXT, permission TEXT, agent TEXT, model TEXT,
	time_created INTEGER NOT NULL DEFAULT 0, time_updated INTEGER NOT NULL DEFAULT 0,
	time_compacting INTEGER, time_archived INTEGER
);
CREATE TABLE session_message (
	id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER,
	time_created INTEGER, time_updated INTEGER, data TEXT
);`

// writeV2DB creates a v2 database file through a separate writable
// connection and returns its path.

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}

func writeV2DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Exec(v2SchemaDDL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO session_v2 (id, project_id, directory, title, cost, tokens_input, tokens_output, time_created, time_updated)
		VALUES ('ses_a', 'p1', '/proj', 'Titled', 0.5, 10, 20, 1, 3)`)
	exec(`INSERT INTO session_v2 (id, project_id, directory, title, time_created, time_updated)
		VALUES ('ses_b', 'p1', '/proj', NULL, 1, 2)`)
	msg := func(id, typ string, seq, created int64, data any) {
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO session_message VALUES (?, 'ses_a', ?, ?, ?, ?, ?)`, id, typ, seq, created, created, string(raw))
	}
	msg("msg_0001", "system", 0, 0, map[string]any{"text": "sys", "time": map[string]any{"created": 0}})
	msg("msg_0002", "user", 1, 1, map[string]any{"text": "hi", "time": map[string]any{"created": 1}})
	msg("msg_0003", "assistant", 2, 2, map[string]any{
		"agent":  "build",
		"model":  map[string]any{"providerID": "anthropic", "id": "claude"},
		"time":   map[string]any{"created": 2, "completed": 3},
		"finish": "stop",
		"cost":   0.5,
		"tokens": map[string]any{"input": 10, "output": 20, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}},
		"content": []any{
			map[string]any{"type": "text", "text": "hello"},
			map[string]any{"type": "tool", "id": "call_1", "name": "shell",
				"state": map[string]any{"status": "completed", "input": map[string]any{"command": "ls"},
					"content": []any{map[string]any{"type": "text", "text": "out"}}},
				"time": map[string]any{"created": 2, "completed": 3}},
		},
	})
	return path
}

func TestOpenV2ViewsReadV2Schema(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	d, err := Open(writeV2DB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.V2() {
		t.Fatal("V2() = false, want true")
	}
	ctx := t.Context()

	sessions, err := d.GetSessions(ctx, "", 0)
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	titles := map[string]string{}
	for _, s := range sessions {
		titles[s.ID] = s.Title
	}
	if got, ok := titles["ses_a"]; !ok || got != "Titled" {
		t.Errorf("ses_a title = %q (listed %v), want Titled", got, ok)
	}
	if got, ok := titles["ses_b"]; !ok || got != "" {
		t.Errorf("ses_b title = %q (listed %v), want '' for NULL", got, ok)
	}

	msgs, err := d.GetSessionMessages(ctx, "ses_a")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (system hidden): %+v", len(msgs), msgs)
	}
	wantRoles := map[string]string{"msg_0002": "user", "msg_0003": "assistant"}
	for _, m := range msgs {
		var info struct {
			Role   string `json:"role"`
			Finish string `json:"finish"`
		}
		if err := json.Unmarshal(m.Data, &info); err != nil {
			t.Fatalf("message %s data: %v", m.ID, err)
		}
		if want, ok := wantRoles[m.ID]; !ok || info.Role != want {
			t.Errorf("message %s role = %q, want %q", m.ID, info.Role, want)
		}
		if m.ID == "msg_0003" && info.Finish != "stop" {
			t.Errorf("assistant finish = %q, want stop", info.Finish)
		}
	}

	parts, err := d.GetSessionParts(ctx, "ses_a")
	if err != nil {
		t.Fatalf("GetSessionParts: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, p := range parts {
		var body map[string]any
		if err := json.Unmarshal(p.Data, &body); err != nil {
			t.Fatalf("part %s: %v", p.ID, err)
		}
		byID[p.ID] = body
	}
	id := func(msg string, i int) string { return fmt.Sprintf("prt_%s%04d", msg, i) }
	checks := map[string]string{
		id("0002", 1): "text",       // user text
		id("0003", 0): "step-start", // synthetic
		id("0003", 1): "text",
		id("0003", 2): "tool",
		id("0003", 3): "step-finish",
	}
	for pid, typ := range checks {
		if got := byID[pid]["type"]; got != typ {
			t.Errorf("part %s type = %v, want %s (parts: %v)", pid, got, typ, keys(byID))
		}
	}
	if tool := byID[id("0003", 2)]["tool"]; tool != "bash" {
		t.Errorf("tool name = %v, want bash", tool)
	}
	if len(byID) != len(checks) {
		t.Errorf("got %d parts, want %d: %v", len(byID), len(checks), keys(byID))
	}
}

func keys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestOpenV1InstalledSkipsV2Views(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	d, err := Open(writeV2DB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.V2() {
		t.Fatal("V2() = true, want false")
	}
	if _, err := d.GetSessions(t.Context(), "", 0); err == nil {
		t.Fatal("GetSessions succeeded without views on a v2-only schema; want an error")
	}
}

func TestOpenV2InstalledOnV1SchemaDoesNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`
		CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '', parent_id TEXT,
			title TEXT NOT NULL DEFAULT '', directory TEXT NOT NULL DEFAULT '',
			time_created INTEGER NOT NULL DEFAULT 0, time_updated INTEGER NOT NULL DEFAULT 0,
			summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, share_url TEXT);
		CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}');
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}');
		INSERT INTO session (id, title, directory, time_created, time_updated) VALUES ('s1', 'v1 title', '/p', 1, 2);
		INSERT INTO message (id, session_id, time_created, data) VALUES ('m1', 's1', 1, '{"role":"user"}');
	`); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	defer ocv2.SetInstalledV2(true)()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	sessions, err := d.GetSessions(t.Context(), "", 0)
	if err != nil {
		t.Fatalf("GetSessions on v1 schema with v2 installed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Title != "v1 title" {
		t.Fatalf("sessions = %+v, want the v1 row", sessions)
	}
	msgs, err := d.GetSessionMessages(t.Context(), "s1")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("GetSessionMessages = %v, %v; want 1 message", msgs, err)
	}
}

// A view column referenced several times in one query converts the row
// once; a changed row converts again.
func TestV2ViewConversionIsMemoized(t *testing.T) {
	calls := 0
	convert := func() (driver.Value, error) { calls++; return "x", nil }
	args := []driver.Value{"msg_1", "ses_1", "assistant", []byte(`{"a":1}`)}
	for range 3 {
		if v, err := memoized("message", args, convert); err != nil || v != "x" {
			t.Fatalf("memoized = %v, %v", v, err)
		}
	}
	changed := []driver.Value{"msg_1", "ses_1", "assistant", []byte(`{"a":2}`)}
	_, _ = memoized("message", changed, convert)
	_, _ = memoized("parts", args, convert)
	if calls != 3 {
		t.Fatalf("conversions = %d, want 3 (first, changed row, other function)", calls)
	}
}
