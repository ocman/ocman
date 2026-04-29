package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestRunShellOnPort_PostsExpectedBody verifies that the OpenCode
// adapter targets POST /session/{id}/shell with the documented
// {command, agent} body shape, hard-coding agent="build" per the
// composer's `!`-prefix design.
func TestRunShellOnPort_PostsExpectedBody(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := runShellOnPort(context.Background(), port, platforms.RunShellRequest{
		SessionID: "ses_abc",
		Command:   "echo hi",
		Agent:     "build",
	})
	if err != nil {
		t.Fatalf("runShellOnPort: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/session/ses_abc/shell" {
		t.Errorf("path = %q, want /session/ses_abc/shell", gotPath)
	}
	if gotBody["command"] != "echo hi" {
		t.Errorf("body.command = %q, want %q", gotBody["command"], "echo hi")
	}
	if gotBody["agent"] != "build" {
		t.Errorf("body.agent = %q, want %q", gotBody["agent"], "build")
	}
}

// TestRunShellOnPort_DefaultsAgentToBuild ensures we never POST a
// blank agent (OpenCode's Zod schema rejects that with a 400). When
// the caller passes Agent="" we fill in "build" — the spec ocman
// committed to with the user.
func TestRunShellOnPort_DefaultsAgentToBuild(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := runShellOnPort(context.Background(), port, platforms.RunShellRequest{
		SessionID: "ses_abc",
		Command:   "ls",
		// Agent intentionally blank.
	})
	if err != nil {
		t.Fatalf("runShellOnPort: %v", err)
	}
	if gotBody["agent"] != "build" {
		t.Errorf("agent = %q, want %q (default)", gotBody["agent"], "build")
	}
}

// TestRunShellOnPort_RejectsEmptyCommand guards against shipping an
// empty `!` to OpenCode (which would 400 with a less helpful error).
// Empty / whitespace-only commands are caller errors.
func TestRunShellOnPort_RejectsEmptyCommand(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	for _, cmd := range []string{"", "   ", "\t\n"} {
		err := runShellOnPort(context.Background(), port, platforms.RunShellRequest{
			SessionID: "ses_abc",
			Command:   cmd,
			Agent:     "build",
		})
		if err == nil {
			t.Errorf("runShellOnPort(%q): expected error, got nil", cmd)
		}
	}
	if called {
		t.Error("upstream was called for an empty command; should have short-circuited")
	}
}

// TestRunShellOnPort_PropagatesUpstreamRejection ensures a 4xx upstream
// reply is converted into a typed *platforms.UpstreamError so the
// HTTP layer maps it to 422 with the parsed message — same contract
// as SendMessage / ExecuteCommand.
func TestRunShellOnPort_PropagatesUpstreamRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name":"BadRequest","data":{"message":"agent missing"}}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := runShellOnPort(context.Background(), port, platforms.RunShellRequest{
		SessionID: "ses_abc",
		Command:   "echo hi",
		Agent:     "build",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Errorf("error does not wrap ErrUpstreamRejected: %v", err)
	}
}
