package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type canonicalRemotePlatform struct{ *fakePlatform }

func (*canonicalRemotePlatform) CanonicalSessionIDs() bool { return true }

// The server's own session service, not a test-built service, must decide
// ownership from the catalogue. A stale reverse cache cannot redirect a
// mutation to another owner, and the browser is told to resolve legacy links.
func TestServerSessionServiceUsesCanonicalOwnerAuthority(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	var localAborts, remoteAborts []string
	native := &canonicalMetadataNative{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "root", Directory: "/repo", Title: "root"}}}}
	native.abortFn = func(req platforms.AbortRequest) error { localAborts = append(localAborts, req.SessionID); return nil }
	history := statev2test.Open(t)
	adapter, err := owned.New(t.Context(), native, catalogue, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	remote := &canonicalRemotePlatform{fakePlatform: &fakePlatform{id: "r-other:opencode"}}
	remote.abortFn = func(req platforms.AbortRequest) error { remoteAborts = append(remoteAborts, req.SessionID); return nil }
	registry := platforms.NewRegistry()
	registry.Register(adapter)
	registry.Register(remote)
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", registry, nil).WithConversationHistory(history, "owner")
	ingestHistory(t, s, history, 1)
	root, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "source", NativeID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	registry.RememberSessions(remote.ID(), []db.Session{{ID: root.ID}})
	if err := s.SessionService().Abort(t.Context(), "", platforms.AbortRequest{SessionID: root.ID}); err != nil {
		t.Fatal(err)
	}
	if len(remoteAborts) != 0 || len(localAborts) != 1 || localAborts[0] != "root" {
		t.Fatalf("reverse cache decided owner authority: local=%v remote=%v", localAborts, remoteAborts)
	}
	rec := httptest.NewRecorder()
	s.handleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var capabilities struct {
		OwnedSessionIDs bool `json:"ownedSessionIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &capabilities); err != nil || !capabilities.OwnedSessionIDs {
		t.Fatalf("canonical server hid owned session IDs from the browser: %s %v", rec.Body, err)
	}
}
