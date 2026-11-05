package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/remote"
)

func ownerWatchEventFixture(t *testing.T) (*Server, *Server, context.Context) {
	t.Helper()
	owner, hub := testServer(t), testServer(t)
	owner.registry.Register(&fakePlatform{PlatformID: "fake", SessionFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id}}, nil
	}})
	link := prworkflow.Link{Platform: "fake", SessionID: "s1", RemoteID: "local", Watching: true, Number: 42, Remote: forge.Remote{Host: "github.com", Repo: "team/repo"}}
	payload, _ := json.Marshal(link)
	if _, err := owner.stateDB.SQL().ExecContext(t.Context(), `INSERT INTO session_pull_request VALUES(?,?,?,?,?,?)`, "fake", "s1", "github.com", "team/repo", 42, string(payload)); err != nil {
		t.Fatal(err)
	}
	rpc := remote.NewServer(owner.registry, owner.router().Local(), "machine", "test").UsePullRequests(owner.RemotePullRequestOperation).
		UseSessionChanges(owner.StreamRemoteSessionChanges)
	listener, err := remote.NewListener(remote.ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, rpc)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	mgr := remote.NewManager(hub.registry, hub.router(), hub.stateDB, "fake")
	t.Cleanup(mgr.Stop)
	if _, err := mgr.Add(t.Context(), "grpc://"+listener.Addr(), "token", "Owner"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	for {
		if _, ok := hub.registry.Get("r-machine:fake"); ok && owner.broadcastHub.subscriberCount() > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("remote did not connect")
		case <-time.After(time.Millisecond):
		}
	}
	return owner, hub, ctx
}

func assertOwnerWatchHubEvent(t *testing.T, ctx context.Context, sub *broadcastSub) {
	t.Helper()
	select {
	case ev := <-sub.ch:
		var change map[string]any
		if err := json.Unmarshal(ev.data, &change); err != nil || ev.event != "ocman.session.changed" || change["platform"] != "r-machine:fake" || change["sessionID"] != "s1" {
			t.Fatalf("unexpected hub event: %+v %v", ev, err)
		}
	case <-ctx.Done():
		t.Fatal("owner stop did not publish a hub invalidation")
	}
}

func TestOwnerWatchStopNotifiesHubWithoutHubMutation(t *testing.T) {
	owner, hub, ctx := ownerWatchEventFixture(t)
	sub, unsub := hub.broadcastHub.subscribe()
	defer unsub()
	r := httptest.NewRequest(http.MethodPost, "/api/session/s1/pull-requests?platform=fake", strings.NewReader(`{"action":"unwatch","host":"github.com","repo":"team/repo","number":42}`))
	w := httptest.NewRecorder()
	serveModule(owner, owner.prWorkflowModule(), w, r)
	if w.Code != 200 {
		t.Fatalf("owner mutation failed: %d %s", w.Code, w.Body.String())
	}
	assertOwnerWatchHubEvent(t, ctx, sub)
}

type terminalWatchForge struct{ forge.Forge }

func (terminalWatchForge) LookupPR(context.Context, string, int) (forge.PR, error) {
	return forge.PR{Number: 42, Status: "merged"}, nil
}

func TestOwnerAutomaticWatchStopNotifiesHubWithoutHubPoll(t *testing.T) {
	owner, hub, ctx := ownerWatchEventFixture(t)
	sub, unsub := hub.broadcastHub.subscribe()
	defer unsub()
	m := prworkflow.New(prworkflow.Deps{
		DB: owner.stateDB, Owner: owner.prOwner, Ready: owner.prWatchReady,
		ResolveForge: func(forge.Remote) (forge.Forge, bool) { return terminalWatchForge{}, true },
		Changed:      func(platform, id string) { owner.broadcastSessionPatch(platform, id, nil) },
	})
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(workerCtx) }()
	defer func() { cancel(); <-done }()
	assertOwnerWatchHubEvent(t, ctx, sub)
	var watching bool
	if err := owner.stateDB.SQL().QueryRowContext(ctx, `SELECT json_extract(payload, '$.watching') FROM session_pull_request`).Scan(&watching); err != nil || watching {
		t.Fatalf("automatic stop not persisted: watching=%v err=%v", watching, err)
	}
}
