package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestSessionDetailReturnsCreator(t *testing.T) {
	for _, platform := range []string{"opencode", "r-machine:opencode"} {
		t.Run(platform, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			reg.Register(&fakePlatform{PlatformID: platform, SessionFn: func(id string) (*platforms.SessionDetail, error) {
				return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: platform, ParentID: "native-parent"}}, nil
			}})
			if err := srv.stateDB.RecordSessionCreator(t.Context(), state.SessionCreator{
				Platform: platform, SessionID: "child", CreatorPlatform: "claude", CreatorSessionID: "launcher", CreatedAt: 42,
			}); err != nil {
				t.Fatal(err)
			}
			detail, err := (sessionMCPService{srv}).GetSession(t.Context(), platform, "child", 0)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Creator *struct {
					Platform  string `json:"platform"`
					SessionID string `json:"sessionId"`
					CreatedAt int64  `json:"createdAt"`
				} `json:"creator"`
			}
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
			if response.Creator == nil || response.Creator.Platform != "claude" || response.Creator.SessionID != "launcher" || response.Creator.CreatedAt != 42 {
				t.Fatalf("missing creator: %s", data)
			}
			if detail.Session.ParentID != "native-parent" {
				t.Fatal("creator changed native ancestry")
			}
			req := httptest.NewRequest(http.MethodGet, "/api/session/child?platform="+platform, nil)
			req.SetPathValue("id", "child")
			rr := httptest.NewRecorder()
			srv.handleSession(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("REST detail: %d %s", rr.Code, rr.Body.String())
			}
			response.Creator = nil
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Creator == nil || response.Creator.SessionID != "launcher" || response.Creator.Platform != "claude" {
				t.Fatalf("REST creator missing: %s", rr.Body.String())
			}
		})
	}
}

func TestSessionCreatorResponseScopeAndMissingState(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	if err := srv.stateDB.RecordSessionCreator(t.Context(), state.SessionCreator{
		Platform: "opencode", SessionID: "child", CreatorPlatform: "claude", CreatorSessionID: "launcher",
	}); err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"claude", "r-machine:opencode"} {
		detail := &platforms.SessionDetail{}
		srv.enrichSessionCreator(t.Context(), platform, "child", detail)
		assertNoCreator(t, detail)
	}
	detail := &platforms.SessionDetail{}
	srv.enrichSessionCreator(t.Context(), "opencode", "unlinked", detail)
	assertNoCreator(t, detail)
	// Creator-read failures must not make the stored conversation inaccessible.
	if _, err := srv.stateDB.SQL().Exec(`DROP TABLE session_creator`); err != nil {
		t.Fatal(err)
	}
	srv.enrichSessionCreator(t.Context(), "opencode", "child", detail)
	assertNoCreator(t, detail)
	srv.stateDB = nil
	srv.enrichSessionCreator(t.Context(), "opencode", "child", detail)
	assertNoCreator(t, detail)
}

func assertNoCreator(t *testing.T, detail *platforms.SessionDetail) {
	t.Helper()
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["creator"]; ok {
		t.Fatalf("invented creator: %s", data)
	}
}
