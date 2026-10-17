package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestSessionListFailureLogging(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		result   bool
		warnings int
	}{
		{"dashboard failure", errors.New("upstream unavailable"), false, 1},
		{"dashboard cancellation", fmt.Errorf("listing: %w", context.Canceled), false, 0},
		{"dashboard deadline", context.DeadlineExceeded, false, 1},
		{"push caller owns failure logging", context.DeadlineExceeded, true, 0},
		{"success", nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{registry: platforms.NewRegistry()}
			srv.registry.Register(&fakePlatform{id: "fake", sessionsHook: func(context.Context, string, int64) ([]db.Session, error) {
				return nil, tc.err
			}})
			logger := log.StandardLogger()
			oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
			t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
			hook := logtest.NewLocal(logger)
			if tc.result {
				_, err := srv.fanOutSessionsResult(context.Background(), "", 0, nil)
				if !errors.Is(err, tc.err) {
					t.Fatalf("error = %v, want %v", err, tc.err)
				}
				if got, want := err.Error(), "listing fake sessions: "+tc.err.Error(); got != want {
					t.Fatalf("error text = %q, want %q", got, want)
				}
			} else {
				srv.fanOutSessions(context.Background(), "", 0, nil)
			}
			var warnings int
			for _, entry := range hook.AllEntries() {
				if entry.Level == log.WarnLevel {
					warnings++
				}
			}
			if warnings != tc.warnings {
				t.Fatalf("warnings = %d, want %d", warnings, tc.warnings)
			}
		})
	}
}

func TestSessionListCancellationDoesNotHideOtherPlatformFailure(t *testing.T) {
	srv := &Server{registry: platforms.NewRegistry()}
	for id, failure := range map[string]error{"canceled": context.Canceled, "failed": errors.New("upstream unavailable")} {
		srv.registry.Register(&fakePlatform{id: id, sessionsHook: func(context.Context, string, int64) ([]db.Session, error) {
			return nil, failure
		}})
	}
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	hook := logtest.NewLocal(logger)
	srv.fanOutSessions(context.Background(), "", 0, nil)
	if entries := hook.AllEntries(); len(entries) != 1 || entries[0].Level != log.WarnLevel {
		t.Fatalf("entries = %v, want only the real failure warning", entries)
	}
}

func TestSessionListUnavailableAndStaleAdaptersStayQuiet(t *testing.T) {
	srv := &Server{registry: platforms.NewRegistry()}
	srv.registry.Register(&fakePlatform{id: "offline", available: new(false)})
	srv.registry.Register(&fakePlatform{id: "stale", sessions: []db.Session{{ID: "old", Stale: true}}})
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	hook := logtest.NewLocal(logger)
	sessions := srv.fanOutSessions(context.Background(), "", 0, nil)
	if len(sessions) != 1 || sessions[0].ID != "old" {
		t.Fatalf("sessions = %v, want stale snapshot", sessions)
	}
	if got := len(hook.AllEntries()); got != 0 {
		t.Fatalf("log entries = %d, want 0", got)
	}
}
