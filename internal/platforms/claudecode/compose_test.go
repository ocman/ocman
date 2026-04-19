package claudecode

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// platformSendReq builds a SendMessageRequest — small helper so the
// adapter-level tests don't each hardcode the full struct literal.
func platformSendReq(id, msg string) platforms.SendMessageRequest {
	return platforms.SendMessageRequest{SessionID: id, Message: msg}
}

// fakeSpawner captures invocation args without actually spawning a
// process. Tests assert against .args + .cwd.
type fakeSpawner struct {
	args []string
	cwd  string
	err  error
}

func (f *fakeSpawner) spawn(_ context.Context, cwd string, args []string) error {
	f.cwd = cwd
	f.args = args
	return f.err
}

func TestSendPrompt_BuildsCorrectCommand(t *testing.T) {
	f := &fakeSpawner{}
	err := sendPromptWith(
		context.Background(), f,
		"abc-uuid", "/tmp/proj", "hello world",
	)
	if err != nil {
		t.Fatalf("sendPromptWith: %v", err)
	}
	if f.cwd != "/tmp/proj" {
		t.Errorf("cwd = %q, want /tmp/proj", f.cwd)
	}
	// Expected argv: [claude, -p, --resume, abc-uuid, hello world]
	want := []string{"claude", "-p", "--resume", "abc-uuid", "hello world"}
	if len(f.args) != len(want) {
		t.Fatalf("args = %v, want %v", f.args, want)
	}
	for i := range want {
		if f.args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, f.args[i], want[i])
		}
	}
}

func TestSendPrompt_RejectsEmptySession(t *testing.T) {
	f := &fakeSpawner{}
	err := sendPromptWith(context.Background(), f, "", "/tmp", "hi")
	if err == nil {
		t.Error("expected error for empty session id")
	}
}

func TestSendPrompt_RejectsEmptyPrompt(t *testing.T) {
	f := &fakeSpawner{}
	err := sendPromptWith(context.Background(), f, "s1", "/tmp", "")
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestSendPrompt_RejectsEmptyCwd(t *testing.T) {
	f := &fakeSpawner{}
	err := sendPromptWith(context.Background(), f, "s1", "", "hi")
	if err == nil {
		t.Error("expected error for empty cwd")
	}
}

func TestSendPrompt_PropagatesSpawnError(t *testing.T) {
	f := &fakeSpawner{err: errors.New("boom")}
	err := sendPromptWith(context.Background(), f, "s1", "/tmp", "hi")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected wrapped spawn error, got %v", err)
	}
}

// TestExecSpawner_RejectsMissingBinary is the one real-process test:
// execSpawner with a bogus binary name should return exec.ErrNotFound.
// Covers the production path without needing claude on PATH.
func TestExecSpawner_RejectsMissingBinary(t *testing.T) {
	s := &execSpawner{binary: "definitely-not-a-real-binary-xyz"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := s.spawn(ctx, "/tmp", []string{"definitely-not-a-real-binary-xyz"})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !errors.Is(err, exec.ErrNotFound) && !strings.Contains(err.Error(), "not found") {
		t.Errorf("error not clearly 'not found': %v", err)
	}
}

// TestAdapter_SendMessage_ResolvesCwdFromSession exercises the full
// adapter path: SendMessage looks up the session to recover cwd,
// then spawns. Uses a real jsonl fixture + fake sender so the test
// is hermetic.
func TestAdapter_SendMessage_ResolvesCwdFromSession(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"-Users-dries-src-proj/S1.jsonl": sampleJSONL,
	})
	f := &fakeSpawner{}
	a := NewFromDir(root).WithSender(f)

	err := a.SendMessage(context.Background(), platformSendReq("S1", "hello"))
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// sampleJSONL has cwd=/Users/dries/src/proj — SendMessage must
	// pass that through to the spawner.
	if f.cwd != "/Users/dries/src/proj" {
		t.Errorf("spawner cwd = %q, want /Users/dries/src/proj", f.cwd)
	}
	wantArgs := []string{"claude", "-p", "--resume", "S1", "hello"}
	if len(f.args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", f.args, wantArgs)
	}
	for i, w := range wantArgs {
		if f.args[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, f.args[i], w)
		}
	}
}

func TestAdapter_SendMessage_RejectsUnknownSession(t *testing.T) {
	root := t.TempDir()
	a := NewFromDir(root).WithSender(&fakeSpawner{})
	err := a.SendMessage(context.Background(), platformSendReq("nope", "hi"))
	if err == nil {
		t.Error("expected error for unknown session")
	}
}
