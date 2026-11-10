package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestNamedConnectionCapabilitiesKeepAccountKeysAndNativeKinds(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	for i, name := range []string{"Work", "Personal"} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: name, Configuration: "isolated", Directory: t.TempDir(), Enabled: i == 0})
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.RegisterConnection(platforms.ID(c.ID), &platformtest.Fake{PlatformID: "claudecode"}); err != nil {
			t.Fatal(err)
		}
	}
	s := New(nil, core, "127.0.0.1:0", registry, nil)
	s.connectionStore = store
	s.connections = platformconnections.NewRouter(store)
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var response struct {
		Platforms []capabilityEntry `json:"platforms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Platforms) != 2 {
		t.Fatal(response.Platforms)
	}
	for i, p := range response.Platforms {
		if p.Kind != "claudecode" || p.ID == p.Kind || p.DisplayName != []string{"Work", "Personal"}[i] || p.Available != (i == 0) {
			t.Fatalf("connection namespace lost: %+v", p)
		}
	}
}

func TestNamedDetailPublishesConnectionKeyAfterNativeStateEnrichment(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	registry := platforms.NewRegistry()
	p := &platformtest.Fake{PlatformID: "claudecode", SessionList: []db.Session{{ID: "session", Platform: "claudecode"}}, SessionFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: "claudecode", Directory: "/repo"}, SessionTree: []db.Session{{ID: id, Platform: "claudecode"}}}, nil
	}}
	if err := registry.RegisterConnection("cnx_work", p); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", registry, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/session/session?platform=cnx_work", nil)
	request.SetPathValue("id", "session")
	w := httptest.NewRecorder()
	s.handleSession(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	var detail platforms.SessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Session.Platform != "cnx_work" || len(detail.SessionTree) != 1 || detail.SessionTree[0].Platform != "cnx_work" {
		t.Fatalf("native kind escaped public read: %+v", detail)
	}
	if p.ID() != "claudecode" {
		t.Fatal("public mapping rewrote native kind")
	}
}

func TestNamedConnectionServerWiresOwnerAndExactDefault(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	router := platformconnections.NewRouter(store)
	s := New(nil, core, "127.0.0.1:0", registry, nil).WithPlatformConnections(router, store, "owner", hostsvc.ConnectionLifecycle{}, nil)
	if _, err := s.connectionDefault(t.Context()); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatal("fresh collection acquired fallback", err)
	}
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: "Work", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.connectionDefault(t.Context()); err != nil || got != c.ID {
		t.Fatal(got, err)
	}
	if err := store.SetEnabled(t.Context(), c.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.connectionDefault(t.Context()); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal(err)
	}
	local, err := s.resolveConnectionOwner(httptest.NewRequest(http.MethodGet, "/api/platform-connections?remoteId=local", nil))
	if err != nil || local == nil {
		t.Fatal(err)
	}
	if _, err := s.resolveConnectionOwner(httptest.NewRequest(http.MethodGet, "/api/platform-connections?remoteId=missing", nil)); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
	if _, err := s.resolveConnectionOwner(httptest.NewRequest(http.MethodGet, "/api/platform-connections", nil)); !errors.Is(err, platformconnections.ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.connectionLifecycle.Activate(context.Background(), c); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal("unconfigured reload claimed activation", err)
	}
}
