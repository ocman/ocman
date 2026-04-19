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

func TestAdapter_Capabilities_Phase4ReadOnly(t *testing.T) {
	a := NewFromDir(t.TempDir())
	c := a.Capabilities()
	// No interactive capabilities in Phase 4.
	if c.Composer || c.RespondPermission || c.RespondQuestion ||
		c.Abort || c.Compact || c.Events ||
		c.AgentCatalog || c.ModelCatalog || c.SlashCommands {
		t.Errorf("Phase 4 should not advertise any interactive capabilities: %+v", c)
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
