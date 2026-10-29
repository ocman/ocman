package acpexec

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
)

func testOptions(t *testing.T, mode string) Options {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Command: Command{Executable: exe, Args: []string{"-test.run=^TestACPAgentProcess$", "--", mode}},
		Dir:     t.TempDir(), Env: append(os.Environ(), "OCMAN_ACP_TEST_PROCESS=1"),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test",
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func startTestClient(t *testing.T, mode string, handlers Handlers) *Client {
	t.Helper()
	if handlers.Update == nil {
		handlers.Update = func(context.Context, acp.SessionNotification) error { return nil }
	}
	c, err := Start(testContext(t), testOptions(t, mode), handlers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestDefaultCommand(t *testing.T) {
	for _, tt := range []struct {
		id   platforms.ID
		want Command
	}{
		{"opencode", Command{"opencode", []string{"acp"}}},
		{"codex", Command{"codex-acp", nil}},
		{"claudecode", Command{"claude-agent-acp", nil}},
	} {
		t.Run(string(tt.id), func(t *testing.T) {
			got, err := DefaultCommand(tt.id)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, tt.want)
			}
		})
	}
	if _, err := DefaultCommand("unknown"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestExecutionLifecycle(t *testing.T) {
	ctx := testContext(t)
	var updates []acp.SessionNotification // SDK serializes notifications before returning Prompt.
	c := startTestClient(t, "normal", Handlers{Update: func(_ context.Context, p acp.SessionNotification) error {
		updates = append(updates, p)
		return nil
	}})
	if c.Info().AgentInfo.Name != "fake" || !c.Info().AgentCapabilities.LoadSession {
		t.Fatalf("unexpected handshake: %+v", c.Info())
	}
	if _, err := c.Authenticate(ctx, acp.AuthenticateRequest{MethodId: "test-login"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	session, err := c.NewSession(ctx, acp.NewSessionRequest{Cwd: dir})
	if err != nil || session.SessionId != "native-session" {
		t.Fatalf("new: %+v, %v", session, err)
	}
	if _, err := c.ResumeSession(ctx, acp.ResumeSessionRequest{Cwd: dir, SessionId: session.SessionId}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LoadSession(ctx, acp.LoadSessionRequest{Cwd: dir, SessionId: session.SessionId}); err != nil {
		t.Fatal(err)
	}
	response, err := c.Prompt(ctx, acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("hello")}})
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("prompt: %+v, %v", response, err)
	}
	if len(updates) != 3 || updates[0].Update.AgentMessageChunk.Content.Text.Text != "history" ||
		updates[1].Update.AgentMessageChunk.Content.Text.Text != "hello" || updates[2].Update.ToolCall == nil {
		t.Fatalf("missing or reordered updates: %+v", updates)
	}
	for _, u := range updates {
		if u.SessionId != session.SessionId {
			t.Fatalf("native identity changed: %s", u.SessionId)
		}
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { _ = c.Close() })
	}
	wg.Wait()
	select {
	case <-c.Done():
	case <-ctx.Done():
		t.Fatal("connection remained open after Close")
	}
}

func TestCapabilityAndDirectoryGates(t *testing.T) {
	c := startTestClient(t, "no-capabilities", Handlers{})
	ctx := testContext(t)
	if _, err := c.LoadSession(ctx, acp.LoadSessionRequest{}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := c.ResumeSession(ctx, acp.ResumeSessionRequest{}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := c.Authenticate(ctx, acp.AuthenticateRequest{MethodId: "not-advertised"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	c = startTestClient(t, "normal", Handlers{})
	for _, dir := range []string{"", "relative/path"} {
		if _, err := c.NewSession(ctx, acp.NewSessionRequest{Cwd: dir}); err == nil {
			t.Fatal("accepted relative new directory")
		}
		if _, err := c.LoadSession(ctx, acp.LoadSessionRequest{Cwd: dir}); err == nil {
			t.Fatal("accepted relative load directory")
		}
		if _, err := c.ResumeSession(ctx, acp.ResumeSessionRequest{Cwd: dir}); err == nil {
			t.Fatal("accepted relative resume directory")
		}
	}
}

func TestStartupFailures(t *testing.T) {
	for _, mode := range []string{"wrong-version", "initialize-error", "exit"} {
		t.Run(mode, func(t *testing.T) {
			opts := testOptions(t, mode)
			opts.Version = "" // exercise default client identity
			c, err := Start(testContext(t), opts, Handlers{Update: func(context.Context, acp.SessionNotification) error { return nil }})
			if err == nil || c != nil {
				t.Fatalf("got client %v, error %v", c, err)
			}
		})
	}
	handlers := Handlers{Update: func(context.Context, acp.SessionNotification) error { return nil }}
	if _, err := Start(t.Context(), Options{}, handlers); err == nil {
		t.Fatal("accepted missing command")
	}
	opts := testOptions(t, "normal")
	if _, err := Start(t.Context(), opts, Handlers{}); err == nil {
		t.Fatal("accepted missing update handler")
	}
	opts.Command.Executable = filepath.Join(t.TempDir(), "missing")
	if _, err := Start(t.Context(), opts, handlers); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Start(ctx, opts, handlers); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := Start(ctx, testOptions(t, "hang-initialize"), handlers); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestPromptErrorAndDisconnect(t *testing.T) {
	for _, text := range []string{"auth-error", "disconnect"} {
		t.Run(text, func(t *testing.T) {
			c := startTestClient(t, "normal", Handlers{})
			_, err := c.Prompt(testContext(t), acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
			if err == nil {
				t.Fatal("expected failure")
			}
			if text == "auth-error" {
				var rpcErr *acp.RequestError
				if !errors.As(err, &rpcErr) || rpcErr.Code != -32000 {
					t.Fatalf("lost authentication error: %v", err)
				}
			} else {
				select {
				case <-c.Done():
				default:
					t.Fatal("disconnect not reported")
				}
			}
		})
	}
}

func TestCancelActivePrompt(t *testing.T) {
	started := make(chan struct{})
	c := startTestClient(t, "normal", Handlers{Update: func(context.Context, acp.SessionNotification) error {
		close(started)
		return nil
	}})
	ctx := testContext(t)
	result := make(chan error, 1)
	go func() {
		out, err := c.Prompt(ctx, acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("wait")}})
		if err == nil && out.StopReason != acp.StopReasonCancelled {
			err = errors.New("missing cancelled stop reason")
		}
		result <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("prompt never started")
	}
	if err := c.Cancel(ctx, "native-session"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("prompt never settled")
	}
}

func TestHandshakeContextDoesNotOwnProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(testContext(t))
	c, err := Start(ctx, testOptions(t, "normal"), Handlers{Update: func(context.Context, acp.SessionNotification) error { return nil }})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	cancel()
	if _, err := c.NewSession(testContext(t), acp.NewSessionRequest{Cwd: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestPromptContextCancellationDoesNotRetry(t *testing.T) {
	started := make(chan struct{})
	c := startTestClient(t, "normal", Handlers{Update: func(context.Context, acp.SessionNotification) error {
		close(started) // a duplicate prompt/update fails the test
		return nil
	}})
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := c.Prompt(ctx, acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("wait")}})
		result <- err
	}()
	select {
	case <-started:
	case <-testContext(t).Done():
		t.Fatal("prompt never started")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled caller reported success")
		}
	case <-testContext(t).Done():
		t.Fatal("cancelled caller remained blocked")
	}
	if _, err := c.NewSession(testContext(t), acp.NewSessionRequest{Cwd: t.TempDir()}); err != nil {
		t.Fatalf("prompt cancellation ended the whole connection: %v", err)
	}
}

func TestCloseKillsUncooperativeAgent(t *testing.T) {
	c := startTestClient(t, "ignore-eof", Handlers{})
	c.proc.stop(10 * time.Millisecond)
	if c.proc.cmd.ProcessState == nil || !strings.Contains(c.proc.cmd.ProcessState.String(), "killed") {
		t.Fatalf("agent was not killed and reaped: %v", c.proc.cmd.ProcessState)
	}
}
