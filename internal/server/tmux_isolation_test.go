package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/state/statetest"
)

// An agent running inside ocman's managed OpenCode inherits TMUX,
// OPENCODE_DB and the real HOME. The default runtime then targeted the live
// ocman-agents tmux server and session, so a test's stop killed the user's
// OpenCode. TestMain must leave tmux nothing shared to reach.
func TestDefaultRuntimeStaysOffTheUsersTmuxServers(t *testing.T) {
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "tmux.log")
	script := "#!/bin/sh\nprintf 'TMUX_TMPDIR=%s TMUX=%s OPENCODE_DB=%s %s\\n' \"$TMUX_TMPDIR\" \"$TMUX\" \"$OPENCODE_DB\" \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	srv := New(nil, statetest.Open(t, ModuleSchemas()...), "", nil, nil)
	inst, err := srv.runtime.Launch(t.Context(), ocruntime.LaunchSpec{RepoRoot: t.TempDir(), Port: 43210})
	if err != nil {
		t.Fatal(err)
	}
	inst.Endpoint = ""
	if err := srv.runtime.Stop(t.Context(), inst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("tmux calls = %q, want launch and stop", lines)
	}
	tmpdir := os.Getenv("TMUX_TMPDIR")
	for _, line := range lines {
		// Without TMUX_TMPDIR, `-L ocman-agents` is the live /tmp socket.
		if tmpdir == "" || !strings.HasPrefix(line, "TMUX_TMPDIR="+tmpdir+" TMUX= OPENCODE_DB= ") {
			t.Errorf("tmux call reaches a shared server: %s", line)
		}
	}
}
