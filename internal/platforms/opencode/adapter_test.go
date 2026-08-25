package opencode

import (
	"context"
	"testing"
)

func TestAdapter_ID(t *testing.T) {
	a := New(nil, nil)
	if a.ID() != "opencode" {
		t.Errorf("expected ID=opencode, got %q", a.ID())
	}
}

func TestAdapter_DisplayName(t *testing.T) {
	a := New(nil, nil)
	if a.DisplayName() != "OpenCode" {
		t.Errorf("expected DisplayName=OpenCode, got %q", a.DisplayName())
	}
}

func TestAdapter_Capabilities_AllTrue(t *testing.T) {
	a := New(nil, nil)
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
	a := New(nil, nil)
	if a.Available(context.Background()) {
		t.Error("Available should return false when DB is nil")
	}
}

func TestAdapter_LiveStatus_ReturnsNil(t *testing.T) {
	a := New(nil, nil)
	// OpenCode does not track in-memory live state via hooks; it uses
	// port discovery on demand. LiveStatus is always nil.
	if ls := a.LiveStatus("any-session"); ls != nil {
		t.Errorf("LiveStatus should return nil for OpenCode, got %+v", ls)
	}
}

// stubParentLookup is a parentLookup implementation backed by a static
// map. Used to exercise bubbleUpPromptsToParent in isolation.
type stubParentLookup struct {
	parents map[string]string
	err     error
}

func (s stubParentLookup) GetSessionParentIDs(_ context.Context, ids []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if p, ok := s.parents[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

// TestBubbleUpPromptsToParent verifies the per-session bubble-up: a
// subagent's prompted ID is retained while the parent's ID is added so
// both visible session rows can show the pending prompt.
func TestBubbleUpPromptsToParent(t *testing.T) {
	tests := []struct {
		name     string
		prompted map[string]bool
		parents  map[string]string // OpenCode session.parent_id
		want     map[string]bool
	}{
		{
			name:     "empty input passes through",
			prompted: nil,
			want:     nil,
		},
		{
			name:     "top-level session unchanged",
			prompted: map[string]bool{"s1": true},
			parents:  map[string]string{},
			want:     map[string]bool{"s1": true},
		},
		{
			name:     "subagent maps to parent",
			prompted: map[string]bool{"child": true},
			parents:  map[string]string{"child": "parent"},
			want:     map[string]bool{"child": true, "parent": true},
		},
		{
			name:     "mixed parent and subagent retain both keys",
			prompted: map[string]bool{"parent": true, "child": true},
			parents:  map[string]string{"child": "parent"},
			want:     map[string]bool{"parent": true, "child": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := stubParentLookup{parents: tt.parents}
			got := bubbleUpPromptsToParent(t.Context(), tt.prompted, lookup)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("got[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

// TestBubbleUpPromptsToParent_NilLookup tolerates a nil DB (e.g. an
// adapter constructed without one in tests) by returning the original
// map untouched.
func TestBubbleUpPromptsToParent_NilLookup(t *testing.T) {
	prompted := map[string]bool{"s1": true}
	got := bubbleUpPromptsToParent(t.Context(), prompted, nil)
	if len(got) != 1 || !got["s1"] {
		t.Errorf("nil lookup should pass through unchanged, got %v", got)
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

	if !directoryHasLivePort(ports, repo) {
		t.Error("exact project-root match should be live")
	}
	if !directoryHasLivePort(ports, worktree) {
		t.Error("worktree dir should fold to project root and be live")
	}
	if directoryHasLivePort(ports, root+"/other") {
		t.Error("unrelated directory must not be live")
	}
	if directoryHasLivePort(map[string]string{}, worktree) {
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

	if got := lookupPortWithWorktreeFold(ports, repo); got != "4096" {
		t.Errorf("exact project-root match = %q, want 4096", got)
	}
	if got := lookupPortWithWorktreeFold(ports, worktree); got != "4096" {
		t.Errorf("worktree dir should fold to project root, got %q, want 4096", got)
	}
	if got := lookupPortWithWorktreeFold(ports, root+"/other"); got != "" {
		t.Errorf("unrelated directory = %q, want empty", got)
	}
	if got := lookupPortWithWorktreeFold(map[string]string{}, worktree); got != "" {
		t.Errorf("no running instances = %q, want empty", got)
	}
}
