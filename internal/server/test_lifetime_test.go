package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// Handler tests do not call Start, but handlers still admit queue jobs,
// idle-edge subscribers and shared project scans. Give them the same lifetime
// and join-before-database-close ordering as a running server.
func ownTestServer(t *testing.T, srv *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	srv.lifetimeCtx = ctx
	if err := srv.queueModule().Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := srv.sharingModule().Start(ctx); err != nil {
		t.Fatal(err)
	}
	srv.pluginsModule().Bind(ctx) // Bind only; discovery must remain explicit.
	t.Cleanup(func() {
		cancel()
		srv.stopPluginProcesses()
		srv.queueModule().Run(ctx)
		srv.sharingModule().Run(ctx)
		srv.pluginsModule().Run(ctx)
		srv.projects.mu.RLock()
		done, running := srv.projects.done, srv.projects.running
		srv.projects.mu.RUnlock()
		if running {
			<-done
		}
	})
}

func TestTestServerJoinsProjectRefreshBeforeTeardown(t *testing.T) {
	finished := make(chan struct{})
	release := make(chan struct{})
	defer func() { close(release); <-finished }()
	var srv *Server
	t.Run("owner", func(t *testing.T) {
		srv = testServer(t)
		started := make(chan struct{})
		srv.projects.fetch = func() ([]db.ProjectStats, error) {
			ctx := srv.lifetime()
			close(started)
			defer close(finished)
			select {
			case <-ctx.Done():
			case <-release:
				return nil, nil
			}
			// The fixture's DB must still be open when its work unwinds.
			if _, _, err := srv.stateDB.GetSetting(context.Background(), "test-lifetime"); err != nil {
				t.Errorf("DB closed before refresh returned: %v", err)
			}
			return nil, ctx.Err()
		}
		srv.triggerProjectsIndexRefresh()
		<-started
	})
	select {
	case <-finished:
	default:
		t.Error("project refresh outlived the test that owns its database")
	}
}
