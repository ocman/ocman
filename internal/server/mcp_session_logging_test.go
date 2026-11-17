package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestMCPCreateLogsFailureStageWithoutPrompt(t *testing.T) {
	for _, stage := range []string{"worktree eligibility", "worktree setup", "runtime preparation", "session allocation", "first prompt"} {
		t.Run(stage, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			failure := errors.New("diagnostic failure")
			host := &mcpCreateHost{capable: true, base: "main", ensureHost: &ensureHost{ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
				if stage == "runtime preparation" {
					return nil, failure
				}
				return &hostsvc.EnsureRuntimeResult{}, nil
			}}}
			if stage == "worktree eligibility" {
				host.treesErr = failure
			}
			if stage == "worktree setup" {
				host.worktreeErr = failure
			}
			srv.hostRouter = hostsvc.NewRouter(host)
			reg.Register(&fakePlatform{PlatformID: "claudecode", CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
				if stage == "session allocation" {
					return nil, failure
				}
				return &platforms.CreateSessionResponse{ID: "child"}, nil
			}, SendMessageFn: func(platforms.SendMessageRequest) error { return failure }})
			hook := logtest.NewGlobal()
			t.Cleanup(hook.Reset)
			in := internalmcp.CreateSessionRequest{Platform: "claudecode", Directory: "/repo", Prompt: "private prompt text", Title: "private title"}
			if stage != "worktree eligibility" && stage != "worktree setup" {
				worktree := false
				in.Worktree = &worktree
			}
			_, err := (sessionMCPService{srv}).CreateSession(t.Context(), in)
			if !errors.Is(err, failure) {
				t.Fatalf("failure changed: %v", err)
			}
			var entry *log.Entry
			for _, candidate := range hook.AllEntries() {
				if candidate.Message == "MCP session creation failed" {
					entry = candidate
				}
			}
			if entry == nil {
				t.Fatal("underlying launch failure was not logged")
			}
			loggedError, _ := entry.Data["error"].(error)
			if entry.Data["stage"] != stage || !errors.Is(loggedError, failure) || entry.Data["directory"] != "/repo" || entry.Data["platform"] != "claudecode" {
				t.Fatalf("missing failure context: %+v", entry.Data)
			}
			text, err := entry.String()
			if err != nil || strings.Contains(text, in.Prompt) || strings.Contains(text, in.Title) {
				t.Fatalf("private input leaked or log failed: %v", err)
			}
			if stage == "first prompt" && entry.Data["created_session_id"] != "child" {
				t.Fatalf("allocated session missing: %+v", entry.Data)
			}
		})
	}
}
