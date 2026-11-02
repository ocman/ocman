package projectsmod

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestFoldWorktreeProjects(t *testing.T) {
	in := []db.ProjectStats{
		{Directory: "/src/foo", SessionCount: 2, MessageCount: 5, LastUsed: 100, TotalTokensIn: 10},
		{Directory: "/src/.worktrees/foo/feature-a", SessionCount: 1, MessageCount: 3, LastUsed: 200, TotalTokensIn: 4},
		{Directory: "/src/.worktrees/foo/feature-b", SessionCount: 3, MessageCount: 1, LastUsed: 50, TotalTokensIn: 6},
		{Directory: "/src/bar", SessionCount: 1, LastUsed: 70},
		// Same repo path on a remote must stay separate.
		{Directory: "/src/.worktrees/foo/wt", RemoteID: "r1", RemoteName: "box", SessionCount: 9, LastUsed: 999},
	}
	out := foldWorktreeProjects(in)

	byKey := map[string]db.ProjectStats{}
	for _, p := range out {
		byKey[p.RemoteID+"|"+p.Directory] = p
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 folded projects, got %d: %+v", len(out), out)
	}

	foo := byKey["|/src/foo"]
	if foo.SessionCount != 6 || foo.MessageCount != 9 || foo.TotalTokensIn != 20 {
		t.Errorf("foo aggregate wrong: %+v", foo)
	}
	if foo.LastUsed != 200 {
		t.Errorf("foo LastUsed = %d, want 200 (max)", foo.LastUsed)
	}
	if _, ok := byKey["|/src/bar"]; !ok {
		t.Errorf("bar missing from output: %+v", out)
	}

	rem := byKey["r1|/src/foo"]
	if rem.RemoteID != "r1" || rem.SessionCount != 9 {
		t.Errorf("remote foo not kept separate: %+v", rem)
	}
}
