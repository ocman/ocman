package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type gitIdentityPlatform struct {
	*fakePlatform
	identity db.Session
	found    bool
}

func (p *gitIdentityPlatform) CachedSession(string) (db.Session, bool) {
	return p.identity, p.found
}

func TestGitHintDoesNotLookUpTranscriptOrBlockRemoteStream(t *testing.T) {
	const event = "data: " + `{"type":"message.part.updated","properties":{"part":{"id":"p","sessionID":"s1","messageID":"m","type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"git push"}}}}}` + "\n\n"
	blocked := make(chan struct{})
	defer close(blocked)
	adapter := &gitIdentityPlatform{fakePlatform: &fakePlatform{id: "r-box:opencode",
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) { <-blocked; return nil, nil },
		proxyEventsFn: func(_ context.Context, _ string, w io.Writer, _ func()) error {
			_, err := io.WriteString(w, event)
			return err
		},
	}, identity: db.Session{RemoteID: "box", ProjectID: "p", Directory: "/repo"}, found: true}
	reg := platforms.NewRegistry()
	reg.Register(adapter)
	srv := &Server{registry: reg, broadcastHub: newBroadcastHub()}
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.serveSessionEvents(w, httptest.NewRequest(http.MethodGet, "/", nil), "s1", adapter)
	}()
	select {
	case <-done:
		if w.Body.String() != event {
			t.Fatalf("raw event missing: %q", w.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("optional git identity lookup blocked the remote stream")
	}
}

func TestRemoteStreamBroadcastsGitHintAndPreservesRawEvent(t *testing.T) {
	const event = "data: " + `{"type":"message.part.updated","properties":{"part":{"id":"p","sessionID":"s1","messageID":"m","type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"git push"}}}}}` + "\n\n"
	reg := platforms.NewRegistry()
	adapter := &gitIdentityPlatform{fakePlatform: &fakePlatform{id: "r-box:opencode", proxyEventsFn: func(_ context.Context, _ string, w io.Writer, _ func()) error {
		_, err := io.WriteString(w, event)
		return err
	}}, identity: db.Session{RemoteID: "box", ProjectID: "p", Directory: "/repo"}, found: true}
	reg.Register(adapter)
	srv := &Server{registry: reg, broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()
	w := httptest.NewRecorder()
	srv.serveSessionEvents(w, httptest.NewRequest(http.MethodGet, "/api/session/s1/events", nil), "s1", adapter)
	if w.Body.String() != event {
		t.Fatalf("raw stream changed: %q", w.Body.String())
	}
	select {
	case ev := <-sub.ch:
		if ev.event != "ocman.git.command" {
			t.Fatalf("event = %s", ev.event)
		}
	default:
		t.Fatal("git hint missing")
	}
}

func TestBroadcastGitCommand(t *testing.T) {
	for _, tc := range []struct {
		name, owner, action string
		missing, fail       bool
	}{
		{name: "local", action: "commit"},
		{name: "remote", owner: "box", action: "push"},
		{name: "unknown session", action: "push", missing: true},
		{name: "adapter without cached identity", action: "push", fail: true},
		{name: "other action", action: "status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := platforms.NewRegistry()
			id := "fake"
			if tc.owner != "" {
				id = "r-" + tc.owner + ":opencode"
			}
			if tc.fail {
				reg.Register(&fakePlatform{id: id})
			} else {
				reg.Register(&gitIdentityPlatform{fakePlatform: &fakePlatform{id: id}, found: !tc.missing,
					identity: db.Session{ID: "s1", Directory: "/repo", ProjectID: "project", RemoteID: tc.owner}})
			}
			srv := &Server{registry: reg, broadcastHub: newBroadcastHub()}
			sub, unsubscribe := srv.broadcastHub.subscribe()
			defer unsubscribe()
			srv.broadcastGitCommand(t.Context(), id, "s1", tc.action)
			if tc.missing || tc.fail || tc.action == "status" {
				select {
				case ev := <-sub.ch:
					t.Fatalf("unexpected event: %s", ev.event)
				default:
				}
				return
			}
			ev := <-sub.ch
			var payload map[string]string
			if err := json.Unmarshal(ev.data, &payload); err != nil {
				t.Fatal(err)
			}
			owner := tc.owner
			if owner == "" {
				owner = "local"
			}
			if ev.event != "ocman.git.command" || payload["remoteId"] != owner || payload["projectId"] != "project" || payload["action"] != tc.action || payload["sessionID"] != "s1" || payload["directory"] != "/repo" {
				t.Fatalf("unexpected event: %s %s", ev.event, ev.data)
			}
		})
	}
}

func TestBroadcastGitCommandLocalDatabase(t *testing.T) {
	srv, raw := testServerWithRawDB(t)
	if _, err := raw.Exec(`INSERT INTO session (id, directory, project_id) VALUES ('s1', '/repo', 'p')`); err != nil {
		t.Fatal(err)
	}
	srv.broadcastHub = newBroadcastHub()
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()
	srv.broadcastGitCommand(t.Context(), "opencode", "s1", "push")
	ev := <-sub.ch
	var payload map[string]string
	if err := json.Unmarshal(ev.data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["remoteId"] != "local" || payload["projectId"] != "p" {
		t.Fatalf("payload = %v", payload)
	}
	srv.broadcastGitCommand(t.Context(), "opencode", "missing", "push")
	srv.broadcastGitCommand(t.Context(), "opencode", "", "push")
	select {
	case ev := <-sub.ch:
		t.Fatalf("unexpected event: %s", ev.event)
	default:
	}
}
