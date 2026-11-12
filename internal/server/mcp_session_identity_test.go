package server

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestMCPGetSessionRetainsNamedAccountAndCreator(t *testing.T) {
	s, registry := newSessionsTestServer(t)
	p := &fakePlatform{PlatformID: "opencode", SessionFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: "opencode"}, SessionTree: []db.Session{{ID: id, Platform: "opencode"}}}, nil
	}}
	if err := registry.RegisterConnection("cnx_work", p); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.RecordSessionCreator(t.Context(), state.SessionCreator{Platform: "cnx_work", SessionID: "child", CreatorPlatform: "cnx_source", CreatorSessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
	got, err := (sessionMCPService{s}).GetSession(t.Context(), "cnx_work", "child", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.Platform != "cnx_work" || got.SessionTree[0].Platform != "cnx_work" || got.Creator == nil || got.Creator.Platform != "cnx_source" || got.Creator.SessionID != "parent" || got.Session.Creator != got.Creator {
		t.Fatalf("MCP detail lost account-scoped provenance: %+v", got)
	}
}
