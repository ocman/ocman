package server

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
)

func TestHostTmuxSessionsUsesActualPathNotSessionName(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	testutil.WriteScript(t, filepath.Join(bin, "tmux"), "#!/bin/sh\nprintf 'unrelated-name\\t1\\t/repo/.worktrees/task\\n'\n")
	sessions, err := (&Server{}).hostTmuxSessions(t.Context())
	if err != nil || len(sessions) != 1 || sessions[0].Path != "/repo/.worktrees/task" {
		t.Fatalf("host tmux sessions lost actual checkout path: %+v %v", sessions, err)
	}
}
