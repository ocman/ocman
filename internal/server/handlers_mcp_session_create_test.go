package server

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestMCPCreateSession(t *testing.T) {
	srv := testServer(t)
	var ensured string
	srv.hostRouter = hostsvc.NewRouter(&ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:6620", RepoRoot: req.ProjectDir}, nil
	}})
	var created platforms.CreateSessionRequest
	var sent platforms.SendMessageRequest
	sendErr := error(nil)
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode",
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			if id != "ses-caller" {
				return nil, platforms.ErrNotFound
			}
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/src/.worktrees/ocman/feat-x"}}, nil
		},
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			created = req
			return &platforms.CreateSessionResponse{ID: "ses-new"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return sendErr },
	})
	srv.registry = reg
	svc := sessionMCPService{srv}

	// No directory: the caller's worktree folds to its project root.
	got, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Model: "p/m", Agent: "plan", Platform: "opencode", SessionID: "ses-caller"})
	if err != nil || got != (internalmcp.CreatedSession{Platform: "opencode", SessionID: "ses-new", Directory: "/src/ocman"}) {
		t.Fatalf("CreateSession = %#v, %v", got, err)
	}
	if ensured != "/src/ocman" || created.Directory != "/src/ocman" || created.Port != "6620" {
		t.Fatalf("ensured = %q, created = %#v", ensured, created)
	}
	if sent.SessionID != "ses-new" || sent.Message != "go" || sent.Model != "p/m" || sent.Agent != "plan" {
		t.Fatalf("sent = %#v", sent)
	}

	// Explicit directory, no caller: local opencode, directory kept as given.
	if got, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/src/.worktrees/ocman/feat-y"}); err != nil || got.Directory != "/src/.worktrees/ocman/feat-y" || ensured != "/src/ocman" {
		t.Fatalf("explicit directory = %#v, %v, ensured %q", got, err, ensured)
	}

	if _, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Platform: "opencode", SessionID: "missing"}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing caller err = %v", err)
	}
	if _, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/repo", Platform: "nope", SessionID: "x"}); !errors.Is(err, internalmcp.ErrInvalidSessionCreate) {
		t.Fatalf("unknown platform err = %v", err)
	}
	if _, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/repo", Platform: "r-gone:opencode", SessionID: "x"}); !errors.Is(err, internalmcp.ErrInvalidSessionCreate) {
		t.Fatalf("disconnected remote err = %v", err)
	}

	sendErr = errors.New("send failed")
	if got, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/repo"}); err == nil || got.SessionID != "ses-new" {
		t.Fatalf("send failure = %#v, %v", got, err)
	}
}
