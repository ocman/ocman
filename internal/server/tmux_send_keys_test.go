package server

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDefaultTmuxRunner_NewWindowRunsCommandLiterally is a regression
// test for two real bugs:
//
//  1. `tmux send-keys -t <target> "opencode --port 0"` was parsed as
//     the named key `Open` followed by the literal `code --port 0`,
//     making the pane execute `code --port 0` instead of `opencode
//     --port 0`. (Even with `-l` (literal mode) the input was still
//     fragile because the user's shell was running and could race
//     with rc-file prompts.)
//
//  2. The user's interactive shell rc files (mise's "trust this
//     config?" prompt, starship init, etc.) consumed the first
//     keystrokes ocman tried to inject, mangling the command.
//
// The fix bypasses send-keys entirely by passing the command as the
// positional shell-command argument to `tmux new-window` / `new-
// session`. tmux runs that command directly in the new pane, in place
// of the user's shell — no rc files, no prompts, no race.
//
// This test runs against a private tmux server (-L) so it doesn't
// touch the user's running tmux.
func TestDefaultTmuxRunner_NewWindowRunsCommandLiterally(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}

	socket := fmt.Sprintf("ocman-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})

	sessionName := "newwindow-cmd"
	// Start an empty session whose first window just runs `cat` so it
	// stays alive for the duration of the test.
	if err := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", sessionName, "-c", "/tmp", "cat").Run(); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}

	// Open a new window with a literal command. We use a marker
	// string so the test isn't sensitive to PATH-shimming or shell
	// escapes; if tmux runs the command literally, the marker shows
	// up in the pane, full stop.
	const marker = "opencode-literal-marker-OK"
	if err := exec.Command(
		"tmux", "-L", socket,
		"new-window", "-t", sessionName, "-n", "wt-cmd-test",
		"-c", "/tmp",
		"sh", "-c", "printf '%s\\n' '"+marker+"'; sleep 5",
	).Run(); err != nil {
		t.Fatalf("tmux new-window with command: %v", err)
	}

	// Capture pane after a short delay so the printf has time to
	// land. capture-pane is instant; this only waits for the foreground
	// process to write its output.
	time.Sleep(150 * time.Millisecond)
	out, err := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", sessionName+":wt-cmd-test").Output()
	if err != nil {
		t.Fatalf("tmux capture-pane: %v", err)
	}
	pane := string(out)
	if !strings.Contains(pane, marker) {
		t.Errorf("pane content does not include %q\npane:\n%s", marker, pane)
	}

	// And explicitly assert the bug doesn't come back: the pane must
	// not contain the mangled "code --port 0" form.
	if strings.Contains(pane, "code --port 0") {
		t.Errorf("pane contains mangled %q (Open was interpreted as a named key)\npane:\n%s", "code --port 0", pane)
	}
}
