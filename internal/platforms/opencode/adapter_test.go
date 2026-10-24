package opencode

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
)

func TestAdapter_ID(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	if a.ID() != "opencode" {
		t.Errorf("expected ID=opencode, got %q", a.ID())
	}
}

func TestAdapter_DisplayName(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	if a.DisplayName() != "OpenCode" {
		t.Errorf("expected DisplayName=OpenCode, got %q", a.DisplayName())
	}
}

func TestAdapter_Capabilities_AllTrue(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	c := a.Capabilities()
	cases := map[string]bool{
		"Composer":          c.Composer,
		"RespondPermission": c.RespondPermission,
		"RespondQuestion":   c.RespondQuestion,
		"Abort":             c.Abort,
		"Compact":           c.Compact,
		"Fork":              c.Fork,
		"Move":              c.Move,
		"Events":            c.Events,
		"AgentCatalog":      c.AgentCatalog,
		"ModelCatalog":      c.ModelCatalog,
		"SlashCommands":     c.SlashCommands,
	}
	for name, got := range cases {
		if !got {
			t.Errorf("capability %s: expected true for OpenCode in v1, got false", name)
		}
	}
}

func TestAdapter_Available_NilDB(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	if a.Available(context.Background()) {
		t.Error("Available should return false when DB is nil")
	}
}

func TestAdapter_LiveStatus_ReturnsNil(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	// OpenCode does not track in-memory live state via hooks; it uses
	// port discovery on demand. LiveStatus is always nil.
	if ls := a.LiveStatus("any-session"); ls != nil {
		t.Errorf("LiveStatus should return nil for OpenCode, got %+v", ls)
	}
}

func TestFoldWorktreeToProjectRoot(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Real worktree layout (as seen in the live OpenCode DB).
		{"/Users/dries/src/github.com/NoUseFreak/.worktrees/ocman/feat-x", "/Users/dries/src/github.com/NoUseFreak/ocman"},
		{"/Users/dries/src/github.com/NoUseFreak/.worktrees/ocman/feat-x/sub", "/Users/dries/src/github.com/NoUseFreak/ocman"},
		{"/src/.worktrees/repo/slug", "/src/repo"},
		// Not a worktree path -> unchanged.
		{"/Users/dries/src/github.com/NoUseFreak/ocman", "/Users/dries/src/github.com/NoUseFreak/ocman"},
		// Too few components after .worktrees -> unchanged.
		{"/src/.worktrees/repo", "/src/.worktrees/repo"},
		// No distinguishable prefix -> unchanged.
		{".worktrees/repo/slug", ".worktrees/repo/slug"},
		{"", ""},
	}
	for _, c := range cases {
		if got := foldWorktreeToProjectRoot(c.in); got != c.want {
			t.Errorf("foldWorktreeToProjectRoot(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDirectoryHasLivePort is the regression guard for worktree
// liveness (#268 fallout): a worktree session's own directory is never
// a port-map key because its OpenCode instance runs at the main
// checkout. Before the fold fallback this returned false, disabling the
// composer + question prompts for worktree sessions. Ports are keyed by
// normalized (symlink-resolved) directory, so we key on a real path
// that exists on this machine to keep the lookup deterministic.
func TestDirectoryHasLivePort(t *testing.T) {
	root := t.TempDir() // e.g. <tmp>/repo's parent — normalized on disk
	repo := root + "/ocman"
	worktree := root + "/.worktrees/ocman/feat-x"
	ports := map[string]string{normalizePortDirectory(repo): "4096"}

	if !td.directoryHasLivePort(ports, repo) {
		t.Error("exact project-root match should be live")
	}
	if !td.directoryHasLivePort(ports, worktree) {
		t.Error("worktree dir should fold to project root and be live")
	}
	if td.directoryHasLivePort(ports, root+"/other") {
		t.Error("unrelated directory must not be live")
	}
	if td.directoryHasLivePort(map[string]string{}, worktree) {
		t.Error("no running instances -> not live")
	}
}

// TestLookupPortWithWorktreeFold guards the same worktree-fold gap for
// the port-resolution path (used by the live per-session SSE stream and
// the permission-prompt fetch). Before the fold, a worktree directory
// resolved to "" and permission/question prompts for worktree sessions
// could be dropped when the by-session HTTP probe fallback missed.
func TestLookupPortWithWorktreeFold(t *testing.T) {
	root := t.TempDir()
	repo := root + "/ocman"
	worktree := root + "/.worktrees/ocman/feat-x"
	ports := map[string]string{normalizePortDirectory(repo): "4096"}

	if got := td.lookupPortWithWorktreeFold(ports, repo); got != "4096" {
		t.Errorf("exact project-root match = %q, want 4096", got)
	}
	if got := td.lookupPortWithWorktreeFold(ports, worktree); got != "4096" {
		t.Errorf("worktree dir should fold to project root, got %q, want 4096", got)
	}
	if got := td.lookupPortWithWorktreeFold(ports, root+"/other"); got != "" {
		t.Errorf("unrelated directory = %q, want empty", got)
	}
	if got := td.lookupPortWithWorktreeFold(map[string]string{}, worktree); got != "" {
		t.Errorf("no running instances = %q, want empty", got)
	}
}
