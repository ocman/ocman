package server

import (
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// Plugin and other automated sessions must be identifiable in the MCP list,
// whose rows come from platform summaries that carry no catalogue origin.
func TestSessionMCPListCarriesCatalogueOrigin(t *testing.T) {
	srv := testServer(t)
	catalogue, err := sessioncatalog.New(srv.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	plugin, _, err := catalogue.Reserve(t.Context(), sessioncatalog.Reservation{RequestKey: "plugin:p:1", PayloadHash: strings.Repeat("a", 64), OwnerID: "owner", Platform: "mcp-test", Directory: "/repo", Origin: sessioncatalog.Plugin, OriginID: "pentest"})
	if err != nil {
		t.Fatal(err)
	}
	srv.registry.Register(&fakePlatform{PlatformID: "mcp-test", SessionList: []db.Session{
		{ID: plugin.ID, Platform: "mcp-test", Directory: "/repo", TimeUpdated: 300},
		{ID: "native-1", Platform: "mcp-test", Directory: "/repo", TimeUpdated: 200},
		{ID: "routine-1", Platform: "mcp-test", Directory: "/repo", TimeUpdated: 100, Origin: "routine", OriginID: "rtn"},
	}})
	sessions, err := (sessionMCPService{srv}).ListSessions(t.Context(), "")
	if err != nil || len(sessions) != 3 {
		t.Fatalf("ListSessions = %#v, %v", sessions, err)
	}
	got := map[string]string{}
	for _, s := range sessions {
		got[s.ID] = s.Origin + "/" + s.OriginID
	}
	if got[plugin.ID] != "plugin/pentest" || got["native-1"] != "/" || got["routine-1"] != "routine/rtn" {
		t.Fatalf("origins = %v", got)
	}
}
