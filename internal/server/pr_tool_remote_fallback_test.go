package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/remote"
)

func TestPRToolRemoteFallbackCannotAuthorizeLocalMutation(t *testing.T) {
	for _, cachedDirectory := range []string{"", "/other"} {
		t.Run("cached="+cachedDirectory, func(t *testing.T) {
			owner, hub := testServer(t), testServer(t)
			var fail atomic.Bool
			owner.registry.Register(&fakePlatform{PlatformID: "lookup", SessionsFn: func(context.Context, string, int64) ([]db.Session, error) {
				if fail.Load() {
					return nil, errors.New("inventory unavailable")
				}
				if cachedDirectory == "" {
					return nil, nil
				}
				return []db.Session{{ID: "remote-session", Directory: cachedDirectory}}, nil
			}})
			rpc := remote.NewServer(owner.registry, owner.router().Local(), "machine", "test")
			listener, err := remote.NewListener(remote.ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, rpc)
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = listener.Serve() }()
			t.Cleanup(listener.Stop)
			mgr := remote.NewManager(hub.registry, hub.router(), hub.stateDB, "lookup")
			t.Cleanup(mgr.Stop)
			if _, err := mgr.Add(t.Context(), "grpc://"+listener.Addr(), "token", "Owner"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var adapter platforms.Platform
			for adapter == nil {
				adapter, _ = hub.registry.Get("r-machine:lookup")
				if adapter != nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("remote did not connect")
				case <-time.After(time.Millisecond):
				}
			}
			if _, err := adapter.Sessions(ctx, "", 0); err != nil {
				t.Fatal(err)
			}
			for _, entry := range hub.registry.Entries() {
				if entry.Key != "r-machine:lookup" {
					hub.registry.Unregister(entry.Key)
				}
			}
			hub.registry.Register(&fakePlatform{PlatformID: "local-lookup", SessionList: []db.Session{{ID: "local-session", Directory: "/repo"}}, SessionFn: func(id string) (*platforms.SessionDetail, error) {
				return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo"}}, nil
			}})
			link := prworkflow.Link{Platform: "local-lookup", SessionID: "local-session", Directory: "/repo", RemoteID: "local", Watching: true, Number: 42, Remote: forge.Remote{Host: "github.com", Repo: "team/repo"}}
			payload, _ := json.Marshal(link)
			if _, err := hub.stateDB.SQL().ExecContext(ctx, `INSERT INTO session_pull_request VALUES(?,?,?,?,?,?)`, link.Platform, link.SessionID, link.Remote.Host, link.Remote.Repo, link.Number, string(payload)); err != nil {
				t.Fatal(err)
			}
			fail.Store(true)
			// Prove the real remote adapter hides this RPC failure, including an
			// empty cache. The mutation lookup must not use this browsing result.
			if rows, err := adapter.Sessions(ctx, "/repo", 0); err != nil || len(rows) != 0 {
				t.Fatalf("expected empty stale fallback: %+v %v", rows, err)
			}
			var req mcplib.CallToolRequest
			req.Params.Arguments = map[string]any{"action": "unwatch", "directory": "/repo", "host": "github.com", "repo": "team/repo", "number": float64(42)}
			result, err := hub.prWorkflowModule().Tools()[0].Handler(ctx, req)
			if err != nil || !result.IsError {
				t.Errorf("incomplete inventory authorized mutation: %+v %v", result, err)
			}
			var watching bool
			if err := hub.stateDB.SQL().QueryRowContext(ctx, `SELECT json_extract(payload,'$.watching') FROM session_pull_request WHERE platform='local-lookup'`).Scan(&watching); err != nil || !watching {
				t.Fatalf("local watch changed after remote failure: %v %v", watching, err)
			}
		})
	}
}
