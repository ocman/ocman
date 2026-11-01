package server

import (
	"context"
	"sync"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/platforms"
)

// recordedRead is an owner read's context state at the moment of the call.
type recordedRead struct {
	hasDeadline bool
	err         error
}

// lifetimeRecordingPlatform records the context state of every owner read.
type lifetimeRecordingPlatform struct {
	*fakePlatform

	mu    sync.Mutex
	calls []recordedRead
}

func (p *lifetimeRecordingPlatform) record(ctx context.Context) {
	_, ok := ctx.Deadline()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, recordedRead{hasDeadline: ok, err: ctx.Err()})
}

func (p *lifetimeRecordingPlatform) reads() []recordedRead {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedRead(nil), p.calls...)
}

func (p *lifetimeRecordingPlatform) SessionMetadata(ctx context.Context, id string) (*db.Session, error) {
	p.record(ctx)
	return &db.Session{ID: id, Directory: "/repo"}, nil
}

func (p *lifetimeRecordingPlatform) NativeQueued(ctx context.Context, _ string) ([]platforms.NativeQueuedMessage, error) {
	p.record(ctx)
	return nil, nil
}

func (p *lifetimeRecordingPlatform) CancelNativeQueued(context.Context, platforms.CancelNativeQueuedRequest) error {
	return nil
}

// TestAutoApproveCallbacksUseServerLifetime is a regression test: the
// auto-approve watcher's queue and session-directory callbacks ran their
// owner and state.db reads on context.Background(), so they were neither
// bounded nor stopped with the server.
func TestAutoApproveCallbacksUseServerLifetime(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := &lifetimeRecordingPlatform{fakePlatform: &fakePlatform{id: "fake", sessions: []db.Session{mkSession("fake", "s1", "t", 1)}}}
	reg.Register(p)
	deps := srv.autoapproveEngineDeps()

	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"session directory", func() { _, _ = deps.SessionDir("s1") }},
		{"queue changed", func() { deps.BroadcastQueueChanged("fake", "s1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A stopped server makes no live owner read.
			stopped, cancel := context.WithCancel(t.Context())
			cancel()
			srv.lifetimeCtx = stopped
			before := len(p.reads())
			tc.run()
			for _, read := range p.reads()[before:] {
				if read.err == nil {
					t.Error("owner read outlived the server lifetime")
				}
			}

			// A running server reads, bounded.
			srv.lifetimeCtx = t.Context()
			before = len(p.reads())
			tc.run()
			reads := p.reads()[before:]
			if len(reads) == 0 {
				t.Fatal("a running server made no owner read")
			}
			for _, read := range reads {
				if !read.hasDeadline {
					t.Error("owner read has no deadline")
				}
			}
		})
	}
}

// TestForgeCredentialLoadUsesServerLifetime is a regression test: forge
// resolution loaded saved credentials from state.db on
// context.Background(), so a stopped server still read the database.
func TestForgeCredentialLoadUsesServerLifetime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		loaded func(*Server) bool
	}{
		{"resolve", func(s *Server) bool {
			_, ok := s.resolveForge(forge.Remote{Type: forge.RemoteTypeGitHub})
			return ok
		}},
		{"previews", func(s *Server) bool { return s.previewForges().GitHubToken != "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newSessionsTestServer(t)
			srv.integrations = &forgeClients{GitHub: github.NewWithCredentials("https://api.github.com", "tok", nil)}
			stopped, cancel := context.WithCancel(t.Context())
			cancel()
			srv.lifetimeCtx = stopped
			if tc.loaded(srv) {
				t.Fatal("a stopped server loaded forge credentials from state.db")
			}
			srv.lifetimeCtx = t.Context()
			if !tc.loaded(srv) {
				t.Fatal("a running server did not resolve its forge credentials")
			}
		})
	}
}
