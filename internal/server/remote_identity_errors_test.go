package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/google/uuid"
)

type identityRPCHost struct{ hostsvc.Host }

func (identityRPCHost) Projects(context.Context) ([]db.ProjectStats, error) { return nil, nil }

func TestRemoteIdentityHTTPStatusMatchesLocalOwner(t *testing.T) {
	owner, _, _ := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(owner.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := catalogue.Reserve(t.Context(), sessioncatalog.Reservation{RequestKey: "remote-pending", PayloadHash: strings.Repeat("a", 64), OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User})
	if err != nil {
		t.Fatal(err)
	}
	rpc := remote.NewServer(owner.registry, identityRPCHost{}, "owner", "test").UseSessions(owner.SessionService())
	listener, err := remote.NewListener(remote.ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, rpc)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	hub, _, _ := identityHTTPFixture(t)
	hubCatalogue, err := sessioncatalog.New(hub.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	hub.sessions.WithSessionCatalogue(hubCatalogue, "hub")
	manager := remote.NewManager(hub.registry, hub.router(), hub.stateDB, "test")
	t.Cleanup(manager.Stop)
	if _, err := manager.Add(t.Context(), "grpc://"+listener.Addr(), "token", "owner"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := hub.registry.Get("r-owner:test"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote adapter did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	for _, tc := range []struct {
		id   string
		code int
	}{{"ocm_" + uuid.NewString(), 404}, {pending.ID, 409}} {
		for _, target := range []struct {
			server   *Server
			platform string
		}{{owner, "test"}, {hub, "r-owner:test"}} {
			w := httptest.NewRecorder()
			target.server.handleSessionIdentity(w, httptest.NewRequest(http.MethodGet, "/api/session-identity?id="+tc.id+"&platform="+target.platform, nil))
			if w.Code != tc.code {
				t.Fatalf("%s identity error: got %d want %d: %s", target.platform, w.Code, tc.code, w.Body.String())
			}
		}
	}
}
