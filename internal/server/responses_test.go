package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorResponsesClassifyCancellation(t *testing.T) {
	for _, responder := range []struct {
		name string
		fn   func(http.ResponseWriter, string, error)
		code int
	}{
		{"server", serverError, http.StatusInternalServerError},
		{"platform", writePlatformError, http.StatusBadGateway},
		{"session service", writeSessionSvcError, http.StatusBadGateway},
	} {
		for _, failure := range []struct {
			name string
			err  error
			code int
		}{
			{"canceled", context.Canceled, 499},
			{"wrapped cancellation", fmt.Errorf("reading state: %w", context.Canceled), 499},
			{"RPC cancellation", status.Error(codes.Canceled, "request canceled"), 499},
			{"RPC deadline", status.Error(codes.DeadlineExceeded, "request timed out"), responder.code},
			{"deadline", context.DeadlineExceeded, responder.code},
			{"failure", errors.New("database unavailable"), responder.code},
		} {
			t.Run(responder.name+"/"+failure.name, func(t *testing.T) {
				logger := log.StandardLogger()
				oldLevel := logger.GetLevel()
				logger.SetLevel(log.DebugLevel)
				t.Cleanup(func() { logger.SetLevel(oldLevel) })
				oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
				t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
				hook := logtest.NewLocal(logger)
				w := httptest.NewRecorder()
				withRequestTiming(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					responder.fn(w, "reading state", failure.err)
				})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
				if w.Code != failure.code {
					t.Fatalf("status = %d, want %d; body=%s", w.Code, failure.code, w.Body)
				}
				wantLevel := log.ErrorLevel
				if failure.code == 499 {
					wantLevel = log.DebugLevel
				}
				found := false
				for _, entry := range hook.AllEntries() {
					if entry.Message == "reading state" {
						found = true
						if entry.Level != wantLevel {
							t.Errorf("failure logged as %s, want %s", entry.Level, wantLevel)
						}
					}
				}
				if !found {
					t.Error("missing failure log")
				}
			})
		}
	}
}

func TestLocalGitDiffCancellation(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/git/diff?dir="+dir+"&fresh=1", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	srv.handleGitDiff(w, req)
	if w.Code != 499 {
		t.Fatalf("status = %d, want 499; body=%s", w.Code, w.Body)
	}
}

func TestRunningLocalGitDiffCancellation(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"rev-parse", "diff"} {
		t.Run(command, func(t *testing.T) {
			srv := testServer(t)
			dir := t.TempDir()
			gitInitForServerTest(t, dir)
			bin := t.TempDir()
			started := filepath.Join(bin, "started")
			// Block inside the subprocess, not a Host stub. Other commands
			// still use real git so the complete local diff path runs.
			script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = %q ]; then\n  touch %q\n  exec sleep 30\nfi\nexec %q \"$@\"\n", command, started, realGit)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			req := httptest.NewRequest(http.MethodGet, "/api/git/diff?dir="+dir+"&fresh=1", nil).WithContext(ctx)
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				srv.handleGitDiff(w, req)
				close(done)
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				if time.Now().After(deadline) {
					cancel()
					<-done
					t.Fatal("git subprocess did not reach the blocking command")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			<-done
			if w.Code != 499 {
				t.Fatalf("status = %d, want 499; body=%s", w.Code, w.Body)
			}
		})
	}
}

type failedGitDiffHost struct {
	hostsvc.Host
	err error
}

func (h *failedGitDiffHost) GitDiff(context.Context, string, hostsvc.GitDiffOptions) (*git.Diff, error) {
	return nil, h.err
}

func TestReadHandlersClassifyCancellation(t *testing.T) {
	for _, route := range []string{"/api/sessions", "/api/sessions/notify", "/api/inbox", "/api/git/diff?dir=/repo", "/api/project/beads-status?dir=/repo"} {
		t.Run(route, func(t *testing.T) {
			srv, _ := newSessionsTestServer(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			req := httptest.NewRequest(http.MethodGet, route, nil).WithContext(ctx)
			var handler http.HandlerFunc
			switch route {
			case "/api/sessions":
				handler = srv.handleSessions
			case "/api/sessions/notify":
				handler = srv.handleSessionsNotify
			case "/api/inbox":
				handler = srv.handleInbox
			case "/api/git/diff?dir=/repo":
				srv.hostRouter = hostsvc.NewRouter(&failedGitDiffHost{err: fmt.Errorf("git diff: %w", ctx.Err())})
				handler = srv.handleGitDiff
			default:
				srv.hostRouter = hostsvc.NewRouter(&beadsHost{id: "local", err: ctx.Err()})
				handler = srv.handleProjectBeadsStatus
			}
			w := httptest.NewRecorder()
			handler(w, req)
			if w.Code != 499 {
				t.Fatalf("status = %d, want 499; body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestHostReadDeadlinesRemainGatewayErrors(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	for _, route := range []string{"/api/git/diff?dir=/repo", "/api/project/beads-status?dir=/repo"} {
		t.Run(route, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, route, nil)
			if route == "/api/git/diff?dir=/repo" {
				srv.hostRouter = hostsvc.NewRouter(&failedGitDiffHost{err: context.DeadlineExceeded})
				srv.handleGitDiff(w, req)
			} else {
				srv.hostRouter = hostsvc.NewRouter(&beadsHost{id: "local", err: context.DeadlineExceeded})
				srv.handleProjectBeadsStatus(w, req)
			}
			if w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body)
			}
		})
	}
}
