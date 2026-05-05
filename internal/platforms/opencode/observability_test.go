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
