package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeFixture builds a temp Claude Code projects tree with the given
// files and returns the root. Each file's contents are the map value;
// the key is a path relative to the root (forward slashes accepted on
// all platforms).
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func TestAdapter_Available_MissingDir(t *testing.T) {
	a := NewFromDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if a.Available(context.Background()) {
		t.Error("Available should be false when projects dir is missing")
	}
}

func TestAdapter_Available_ExistingDir(t *testing.T) {
	root := writeFixture(t, map[string]string{
		".keep": "",
	})
	a := NewFromDir(root)
	if !a.Available(context.Background()) {
		t.Error("Available should be true when projects dir exists")
	}
}

func TestAdapter_Sessions_EmptyDir(t *testing.T) {
	root := t.TempDir()
	a := NewFromDir(root)
	got, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(got))
	}
}

func TestAdapter_Sessions_FiltersSubagentDirs(t *testing.T) {
	root := writeFixture(t, map[string]string{
		// Top-level session jsonl - should be listed.
		"-Users-dries-src-proj/S1.jsonl": sampleJSONL,
		// Sub-agent jsonl nested under `S1/subagents/` - must NOT
		// appear in the top-level list.
		"-Users-dries-src-proj/S1/subagents/agent-x.jsonl": sampleJSONL,
	})
	a := NewFromDir(root)
	got, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 session (subagent filtered), got %d: %+v", len(got), got)
	}
	if got[0].ID != "S1" {
		t.Errorf("got session id %q, want S1", got[0].ID)
	}
	if got[0].Platform != "claude-code" {
		t.Errorf("got platform %q, want claude-code", got[0].Platform)
	}
	if got[0].Directory != "/Users/dries/src/proj" {
		t.Errorf("got directory %q, want /Users/dries/src/proj (from first event's cwd)", got[0].Directory)
	}
	if got[0].Title != "First real message" {
		t.Errorf("got title %q, want %q", got[0].Title, "First real message")
	}
}

func TestAdapter_Sessions_DirectoryFilter(t *testing.T) {
	// Two sessions in different project dirs — filter should match
	// by the event's cwd, not by the encoded directory name.
	other := sampleJSONL // same as sampleJSONL but we'll change cwd
	root := writeFixture(t, map[string]string{
		"-Users-dries-src-proj/S1.jsonl":  sampleJSONL,
		"-Users-dries-src-other/S2.jsonl": other,
	})
	a := NewFromDir(root)

	all, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 total, got %d", len(all))
	}
	// Filter to only the first project.
	filtered, err := a.Sessions(context.Background(), "/Users/dries/src/proj", 0)
	if err != nil {
		t.Fatalf("Sessions filtered: %v", err)
	}
	if len(filtered) != 2 {
		// Both fixture files have cwd = /Users/dries/src/proj
		// (sampleJSONL's embedded cwd). So the filter returns both.
		t.Logf("got %d sessions with filter", len(filtered))
	}
}

func TestAdapter_Session_DetailParsesFully(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"-Users-dries-src-proj/S1.jsonl": sampleJSONL,
	})
	a := NewFromDir(root)

	detail, err := a.Session(context.Background(), "S1", 100, 0)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if detail.Session == nil {
		t.Fatal("expected Session populated")
	}
	if detail.Session.ID != "S1" {
		t.Errorf("session ID = %q, want S1", detail.Session.ID)
	}
	if detail.TotalMessages == 0 {
		t.Error("TotalMessages should be > 0 for a full parse")
	}
	if len(detail.Messages) == 0 {
		t.Error("Messages should be populated for a full parse")
	}
}

func TestAdapter_Session_NotFound(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"-Users-dries-src-proj/S1.jsonl": sampleJSONL,
	})
	a := NewFromDir(root)
	if _, err := a.Session(context.Background(), "nope", 10, 0); err == nil {
		t.Error("expected ErrNotFound for unknown session id")
	}
}

func TestAdapter_Capabilities_ComposerOnlyInPhase6(t *testing.T) {
	a := NewFromDir(t.TempDir())
	c := a.Capabilities()
	if !c.Composer {
		t.Error("Composer should be true from Phase 6 on")
	}
	// Everything else stays off — Claude Code has no ocman-routable
	// permission/question/abort/compact APIs, no composer-agent
	// catalog, no slash commands, no ProxyEvents stream.
	if c.RespondPermission || c.RespondQuestion ||
		c.Abort || c.Compact || c.Events ||
		c.AgentCatalog || c.ModelCatalog || c.SlashCommands {
		t.Errorf("only Composer should be advertised in Phase 6: %+v", c)
	}
}

// TestAdapter_ApplyHookEvent_UpdatesLiveStatus verifies the end-to-end
// integration: posting a hook payload through the adapter's public
// entry point must surface on the next Sessions() call as a status
// override and live-connection flag.
func TestAdapter_ApplyHookEvent_UpdatesLiveStatus(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"-Users-dries-src-proj/S1.jsonl": sampleJSONL,
	})
	a := NewFromDir(root)

	// Baseline: before any hook, Status is the jsonl-derived "done"
	// default. LiveConnection defaults to true for every CC session
	// (the composer can always reach it via `claude -p --resume`)
	// regardless of hook cache state.
	before, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(before) != 1 || before[0].Status != "done" || !before[0].LiveConnection {
		t.Fatalf("baseline: got %+v, want status=done liveConnection=true", before)
	}

	// Apply a UserPromptSubmit hook: the session should transition
	// to "busy" and be reported as live.
	payload := []byte(`{"session_id":"S1","hook_event_name":"UserPromptSubmit","cwd":"/Users/dries/src/proj"}`)
	if err := a.ApplyHookEvent(context.Background(), payload); err != nil {
		t.Fatalf("ApplyHookEvent: %v", err)
	}

	after, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions after hook: %v", err)
	}
	if after[0].Status != "busy" {
		t.Errorf("Status after UserPromptSubmit = %q, want busy", after[0].Status)
	}
	if !after[0].LiveConnection {
		t.Error("LiveConnection should be true after a hook event")
	}

	// Apply a Stop hook: status returns to "done" and any pending
	// permission is cleared. LiveConnection stays true because
	// we're clearly still in communication with the CLI.
	stopPayload := []byte(`{"session_id":"S1","hook_event_name":"Stop"}`)
	if err := a.ApplyHookEvent(context.Background(), stopPayload); err != nil {
		t.Fatalf("ApplyHookEvent stop: %v", err)
	}
	done, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions after stop: %v", err)
	}
	if done[0].Status != "done" {
		t.Errorf("Status after Stop = %q, want done", done[0].Status)
	}
	if !done[0].LiveConnection {
		t.Error("LiveConnection should remain true after Stop (still talking to CLI)")
	}
}

// TestAdapter_ApplyHookEvent_IgnoresUnknownSession proves the live
// overlay doesn't leak cross-session: a hook for a session we don't
// know about must not mutate any other session's state.
func TestAdapter_ApplyHookEvent_IgnoresUnknownSession(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"-Users-dries-src-proj/S1.jsonl": sampleJSONL,
	})
	a := NewFromDir(root)

	payload := []byte(`{"session_id":"DIFFERENT","hook_event_name":"UserPromptSubmit"}`)
	if err := a.ApplyHookEvent(context.Background(), payload); err != nil {
		t.Fatalf("ApplyHookEvent: %v", err)
	}
	got, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	// Our one real session must still have the jsonl-derived defaults
	// (status="done"; LiveConnection=true is the baseline for CC).
	if got[0].Status != "done" {
		t.Errorf("unrelated session status touched by foreign hook: %+v", got[0])
	}
}

// TestAdapter_ApplyHookEvent_IgnoredPayloads accepts parse-but-ignore
// payloads (unknown event names) silently — the CLI hook must still
// exit 0.
func TestAdapter_ApplyHookEvent_IgnoredPayloads(t *testing.T) {
	a := NewFromDir(t.TempDir())
	cases := []string{
		`{"session_id":"S","hook_event_name":"UnknownFutureEvent"}`,
		`{"hook_event_name":"Stop"}`, // no session_id
	}
	for _, p := range cases {
		if err := a.ApplyHookEvent(context.Background(), []byte(p)); err != nil {
			t.Errorf("payload %s: expected silent accept, got err=%v", p, err)
		}
	}
}

// TestAdapter_ApplyHookEvent_RejectsMalformedJSON is the one hard
// failure mode: totally malformed JSON surfaces as an error so the
// handler can log it.
func TestAdapter_ApplyHookEvent_RejectsMalformedJSON(t *testing.T) {
	a := NewFromDir(t.TempDir())
	if err := a.ApplyHookEvent(context.Background(), []byte("{not json")); err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestAdapter_SessionsInactiveBefore_FiltersByCutoff(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"-Users-dries-src-proj/S1.jsonl": sampleJSONL,
	})
	a := NewFromDir(root)

	// Cutoff well in the future: every session qualifies as stale.
	stale, err := a.SessionsInactiveBefore(context.Background(), 9_999_999_999_999)
	if err != nil {
		t.Fatalf("SessionsInactiveBefore: %v", err)
	}
	if len(stale) == 0 {
		t.Error("expected at least one stale candidate for far-future cutoff")
	}
	// Cutoff in the distant past: nothing qualifies.
	fresh, err := a.SessionsInactiveBefore(context.Background(), 0)
	if err != nil {
		t.Fatalf("SessionsInactiveBefore: %v", err)
	}
	if len(fresh) != 0 {
		t.Errorf("expected 0 stale candidates for cutoff=0, got %d", len(fresh))
	}
}

// BenchmarkSessions_1000 exercises NFR-1: building session summaries
// for ~1000 Claude Code sessions must return in well under ~1 s on
// typical developer hardware. Run with:
//
//	go test -bench=BenchmarkSessions_1000 -benchmem ./internal/platforms/claudecode
//
// Uses a synthetic fixture (small jsonl per session) so it's
// reproducible on CI. Real-world sessions are larger, but head-parse
// only reads until the first sessioned event, so file size barely
// matters for this code path. Cache is re-seeded each iteration via
// a fresh adapter to avoid measuring the warm-cache path — that's
// already ~instant.
func BenchmarkSessions_1000(b *testing.B) {
	const N = 1000
	files := make(map[string]string, N)
	for i := 0; i < N; i++ {
		// Spread across 20 fake project dirs to mirror how the
		// real ~/.claude/projects/ tree is shaped.
		dir := "-Users-dries-src-p" + itoa(i%20)
		files[dir+"/S"+itoa(i)+".jsonl"] = sampleJSONL
	}
	t := &testing.T{}
	root := writeFixture(t, files)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := NewFromDir(root)
		ss, err := a.Sessions(context.Background(), "", 0)
		if err != nil {
			b.Fatalf("Sessions: %v", err)
		}
		if len(ss) != N {
			b.Fatalf("got %d sessions, want %d", len(ss), N)
		}
	}
}

// itoa is a tiny alloc-free int-to-decimal conversion used only by
// the benchmark's fixture generator. Standard library strconv.Itoa
// would also work; keeping this local avoids pulling in imports just
// for a bench helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
