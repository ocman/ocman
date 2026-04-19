package claudecode

import (
	"context"
	"fmt"
	"os/exec"

	log "github.com/sirupsen/logrus"
)

// spawner runs a command in a directory and returns without waiting
// for completion. Factored as an interface so tests can substitute a
// fakeSpawner and assert on invocation args without actually exec'ing
// anything.
type spawner interface {
	spawn(ctx context.Context, cwd string, args []string) error
}

// execSpawner is the production spawner: starts a real subprocess via
// os/exec, detached from this process. binary defaults to "claude"
// but tests can override for missing-binary coverage.
type execSpawner struct {
	binary string
}

// spawn launches args[0] with args[1:] in cwd. Detached from the
// caller's context — we never Wait() for the process and we
// deliberately use exec.Command (not CommandContext) so the
// subprocess outlives the HTTP handler that started it. A
// composer POST returns 204 in milliseconds; if the subprocess were
// tied to r.Context() the kernel would SIGKILL claude before it
// could even connect to Anthropic.
//
// Claude will append to its jsonl and fire hooks back into ocman as
// it runs; completion is observed through that side channel.
func (e *execSpawner) spawn(ctx context.Context, cwd string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("spawn: empty argv")
	}
	// args[0] is the binary name. Use LookPath to give a clear error
	// when claude isn't installed, rather than the cryptic "file not
	// found" from os/exec.
	bin := args[0]
	if e.binary != "" {
		bin = e.binary
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s: %w", bin, err)
	}
	// The ctx argument is accepted for interface symmetry but
	// intentionally not wired into exec — see the doc comment above.
	_ = ctx
	cmd := exec.Command(bin, args[1:]...)
	cmd.Dir = cwd
	// No stdin attached — claude -p reads prompt from positional arg.
	// Stdout/stderr discarded: response is streamed back via hooks +
	// jsonl appends; we don't need the subprocess pipes.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	// Reap the process asynchronously so the kernel doesn't leave
	// zombies. Don't block the caller on completion — composer is
	// fire-and-forget by design.
	go func() {
		if err := cmd.Wait(); err != nil {
			log.WithFields(log.Fields{"bin": bin, "error": err}).
				Debug("claude subprocess exited with error")
		}
	}()
	return nil
}

// sendPromptWith is the core composer entry point, parameterised on a
// spawner so unit tests can substitute a fake. Adapter.SendMessage
// calls this with an execSpawner.
//
// Builds the argv:
//
//	claude -p --resume <session-id> <prompt>
//
// `-p` is non-interactive print mode. `--resume` continues the
// existing session whose jsonl lives at
// ~/.claude/projects/<dir-enc>/<session-id>.jsonl. Claude writes new
// turns to that file, which the read path picks up on the next
// Sessions() / Session() poll.
//
// Does NOT use --output-format stream-json: Phase 6 relies entirely
// on the existing hook pipeline + file polling for updates, so the
// subprocess's own stdout is redundant.
func sendPromptWith(ctx context.Context, s spawner, sessionID, cwd, prompt string) error {
	if sessionID == "" {
		return fmt.Errorf("claudecode: sessionID required")
	}
	if cwd == "" {
		return fmt.Errorf("claudecode: cwd required (for claude -p working directory)")
	}
	if prompt == "" {
		return fmt.Errorf("claudecode: prompt required")
	}
	args := []string{"claude", "-p", "--resume", sessionID, prompt}
	if err := s.spawn(ctx, cwd, args); err != nil {
		return fmt.Errorf("spawn claude: %w", err)
	}
	return nil
}
