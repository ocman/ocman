package opencode

import (
	"context"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// TestAgentCatalog_LogsWarnOnFetchFailure exercises FR-9: when the
// upstream /agent fetch returns nothing useful, ocman must log a
// single WARN line so the maintainer can see why the agent picker is
// suddenly empty in the UI.
func TestAgentCatalog_LogsWarnOnFetchFailure(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	// httptest server that always 500s — every attempt to fetch
	// /agent will fail.
	fake := newOpencodeFake(t)
	fake.sessionStatus = 500
	fake.messagesStatus = 500
	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	a := New(database, nil)
	entries, err := a.AgentCatalog(context.Background(), sid)
	if err != nil {
		t.Fatalf("AgentCatalog: unexpected error: %v", err)
	}
	if entries != nil {
		t.Fatalf("AgentCatalog: want nil entries on failure, got %d", len(entries))
	}

	if !findLog(hook, logrus.WarnLevel, "agent catalog fetch failed") {
		t.Fatalf("expected WARN log line about agent catalog fetch failed; got %d entries", len(hook.AllEntries()))
	}
}

// TestAgentCatalog_MapsOpenCodeFields verifies ocman maps OpenCode's
// /agent schema (mode/hidden/native) onto AgentCatalogEntry so the
// frontend picker can section project agents correctly. Regression for
// the bug where custom/project agents rendered without mode/builtIn
// because ocman read a nonexistent "kind" field.
func TestAgentCatalog_MapsOpenCodeFields(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.agentBody = []byte(`[
		{"name":"build","description":"builder","mode":"primary","native":true},
		{"name":"architect","description":"custom project agent","mode":"primary","native":false},
		{"name":"explore","mode":"subagent","native":true},
		{"name":"title","mode":"primary","hidden":true,"native":true}
	]`)
	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	entries, err := a.AgentCatalog(context.Background(), sid)
	if err != nil {
		t.Fatalf("AgentCatalog: unexpected error: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("want 4 entries, got %d", len(entries))
	}

	byName := map[string]int{}
	for i, e := range entries {
		byName[e.Name] = i
	}

	build := entries[byName["build"]]
	if build.Mode != "primary" || build.Kind != "primary" || !build.BuiltIn || build.Hidden {
		t.Errorf("build: got mode=%q kind=%q builtIn=%v hidden=%v", build.Mode, build.Kind, build.BuiltIn, build.Hidden)
	}

	arch := entries[byName["architect"]]
	if arch.Mode != "primary" || arch.BuiltIn {
		t.Errorf("architect (project agent): got mode=%q builtIn=%v; want mode=primary builtIn=false", arch.Mode, arch.BuiltIn)
	}

	explore := entries[byName["explore"]]
	if explore.Mode != "subagent" {
		t.Errorf("explore: got mode=%q; want subagent", explore.Mode)
	}

	title := entries[byName["title"]]
	if !title.Hidden {
		t.Errorf("title: want hidden=true, got false")
	}
}

// TestAgentCatalog_LogsWarnOnDecodeFailure covers the branch where the
// /agent endpoint responds but the body isn't valid JSON: ocman logs a
// WARN and returns an empty catalog rather than surfacing the error.
func TestAgentCatalog_LogsWarnOnDecodeFailure(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.agentBody = []byte(`{not json`)
	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	a := New(database, nil)
	entries, err := a.AgentCatalog(context.Background(), sid)
	if err != nil {
		t.Fatalf("AgentCatalog: unexpected error: %v", err)
	}
	if entries != nil {
		t.Fatalf("AgentCatalog: want nil entries on decode failure, got %d", len(entries))
	}
	if !findLog(hook, logrus.WarnLevel, "agent catalog decode failed") {
		t.Fatalf("expected WARN log about agent catalog decode failed; got %d entries", len(hook.AllEntries()))
	}
}

// TestSlashCommands_LogsWarnOnFetchFailure mirrors the AgentCatalog
// test for the /command endpoint.
func TestSlashCommands_LogsWarnOnFetchFailure(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.sessionStatus = 500
	fake.messagesStatus = 500
	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	a := New(database, nil)
	entries, err := a.SlashCommands(context.Background(), sid)
	if err != nil {
		t.Fatalf("SlashCommands: unexpected error: %v", err)
	}
	if entries != nil {
		t.Fatalf("SlashCommands: want nil entries on failure, got %d", len(entries))
	}

	if !findLog(hook, logrus.WarnLevel, "slash commands fetch failed") {
		t.Fatalf("expected WARN log line about slash commands fetch failed; got %d entries", len(hook.AllEntries()))
	}
}

// TestConvertOpenCodeMessages_LogsDebugOnSkip verifies that the
// per-call skipped-count summary is emitted at DEBUG when at least
// one message is skipped. The test forces logrus to DebugLevel for
// the duration of the test so the line is captured.
func TestConvertOpenCodeMessages_LogsDebugOnSkip(t *testing.T) {
	prevLevel := logrus.GetLevel()
	logrus.SetLevel(logrus.DebugLevel)
	defer logrus.SetLevel(prevLevel)

	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	input := []map[string]interface{}{
		{"info": map[string]interface{}{"id": "m1", "role": "user"}},
		{"noinfo": true}, // skipped
		{"noinfo": true}, // skipped
	}
	convertOpenCodeMessages(input)

	if !findLog(hook, logrus.DebugLevel, "skipped messages with missing/invalid info") {
		t.Fatalf("expected DEBUG log about skipped messages; got %d entries", len(hook.AllEntries()))
	}
}

// TestConvertOpenCodeMessages_NoLogWhenNothingSkipped is the negative
// counterpart — a clean batch produces no log line so the per-call
// summary doesn't pollute happy-path logs.
func TestConvertOpenCodeMessages_NoLogWhenNothingSkipped(t *testing.T) {
	prevLevel := logrus.GetLevel()
	logrus.SetLevel(logrus.DebugLevel)
	defer logrus.SetLevel(prevLevel)

	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	input := []map[string]interface{}{
		{"info": map[string]interface{}{"id": "m1", "role": "user"}},
	}
	convertOpenCodeMessages(input)

	if findLog(hook, logrus.DebugLevel, "skipped messages") {
		t.Fatalf("expected no skip-summary log line on a clean batch")
	}
}

func findLog(hook *logtest.Hook, level logrus.Level, substr string) bool {
	for _, e := range hook.AllEntries() {
		if e.Level == level && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}
