package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// shareTestDetail builds a minimal SessionDetail for the fake platform.
func shareTestDetail(id string) *platforms.SessionDetail {
	return &platforms.SessionDetail{
		Session: &db.Session{ID: id, Title: "Shared Convo", Platform: "fake"},
		Messages: []db.Message{
			{ID: "m1", SessionID: id, TimeCreated: 100, Data: []byte(`{"role":"user"}`)},
		},
		Parts: []db.Part{
			{ID: "p1", MessageID: "m1", SessionID: id, TimeCreated: 100, Data: []byte(`{"type":"text","text":"hello world"}`)},
		},
	}
}

func registerShareFake(reg *platforms.Registry, id string) {
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: id, Platform: "fake"}},
		sessionDetailFn: func(sid string) (*platforms.SessionDetail, error) {
			return shareTestDetail(sid), nil
		},
	})
}

func TestExportMarkdownEndpoint(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_export")

	req := httptest.NewRequest(http.MethodGet, "/api/session/ses_export/export.md", nil)
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("content-type = %q, want text/markdown", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "ses_export") {
		t.Errorf("content-disposition = %q, missing session id", cd)
	}
	if !strings.Contains(rr.Body.String(), "hello world") {
		t.Errorf("body missing conversation text:\n%s", rr.Body)
	}
}

func TestCreateListRevokeShareLink(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_share")

	// Create.
	createReq := httptest.NewRequest(http.MethodPost, "/api/session/ses_share/share", nil)
	createReq.Host = "example.test:9999"
	createRR := httptest.NewRecorder()
	srv.dispatchSessionSubpath(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRR.Code, createRR.Body)
	}
	var created shareLinkView
	if err := json.Unmarshal(createRR.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.Token == "" {
		t.Fatal("expected a token")
	}
	if !strings.Contains(created.URL, "/share/"+created.Token) {
		t.Errorf("share URL %q does not contain the token path", created.URL)
	}
	if !strings.Contains(created.URL, "example.test:9999") {
		t.Errorf("share URL %q did not derive host from request", created.URL)
	}

	// List shows it.
	listReq := httptest.NewRequest(http.MethodGet, "/api/session/ses_share/shares", nil)
	listRR := httptest.NewRecorder()
	srv.dispatchSessionSubpath(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listRR.Code)
	}
	var list []shareLinkView
	if err := json.Unmarshal(listRR.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 || list[0].Token != created.Token {
		t.Fatalf("expected 1 link matching created token, got %+v", list)
	}

	// Revoke.
	revReq := httptest.NewRequest(http.MethodDelete, "/api/session/ses_share/share/"+created.Token, nil)
	revRR := httptest.NewRecorder()
	srv.dispatchSessionSubpath(revRR, revReq)
	if revRR.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204; body=%s", revRR.Code, revRR.Body)
	}

	// List is now empty.
	listRR2 := httptest.NewRecorder()
	srv.dispatchSessionSubpath(listRR2, httptest.NewRequest(http.MethodGet, "/api/session/ses_share/shares", nil))
	var list2 []shareLinkView
	_ = json.Unmarshal(listRR2.Body.Bytes(), &list2)
	if len(list2) != 0 {
		t.Fatalf("expected 0 links after revoke, got %d", len(list2))
	}
}

func TestSharingSettingToggleAndGuard(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_guard")

	// Default: enabled.
	getRR := httptest.NewRecorder()
	srv.handleSharingSetting(getRR, httptest.NewRequest(http.MethodGet, "/api/settings/sharing", nil))
	if getRR.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", getRR.Code)
	}
	var got struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(getRR.Body.Bytes(), &got)
	if !got.Enabled {
		t.Fatal("sharing should be enabled by default")
	}

	// Disable it.
	postRR := httptest.NewRecorder()
	srv.handleSharingSetting(postRR, httptest.NewRequest(http.MethodPost, "/api/settings/sharing", strings.NewReader(`{"enabled":false}`)))
	if postRR.Code != http.StatusOK {
		t.Fatalf("post status = %d, want 200; body=%s", postRR.Code, postRR.Body)
	}
	if srv.sharingEnabled() {
		t.Fatal("sharing should be disabled after POST")
	}

	// Create is now rejected with 403.
	createRR := httptest.NewRecorder()
	srv.dispatchSessionSubpath(createRR, httptest.NewRequest(http.MethodPost, "/api/session/ses_guard/share", nil))
	if createRR.Code != http.StatusForbidden {
		t.Fatalf("create status = %d, want 403 when disabled; body=%s", createRR.Code, createRR.Body)
	}

	// Re-enable and create succeeds.
	reEnable := httptest.NewRecorder()
	srv.handleSharingSetting(reEnable, httptest.NewRequest(http.MethodPost, "/api/settings/sharing", strings.NewReader(`{"enabled":true}`)))
	create2 := httptest.NewRecorder()
	srv.dispatchSessionSubpath(create2, httptest.NewRequest(http.MethodPost, "/api/session/ses_guard/share", nil))
	if create2.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 after re-enable; body=%s", create2.Code, create2.Body)
	}
}

func TestAllSharesGlobalList(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_a")

	if _, err := srv.stateDB.CreateShareLink("fake", "ses_a", 0); err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if _, err := srv.stateDB.CreateShareLink("fake", "ses_b", 0); err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.handleAllShares(rr, httptest.NewRequest(http.MethodGet, "/api/shares", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var list []globalShareLinkView
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 links across sessions, got %d", len(list))
	}
	// Each carries the owning session + a usable URL.
	for _, v := range list {
		if v.SessionID == "" || v.Platform != "fake" || !strings.Contains(v.URL, "/share/") {
			t.Errorf("incomplete global view: %+v", v)
		}
	}
}

func TestPublicShareView(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_pub")

	link, err := srv.stateDB.CreateShareLink("fake", "ses_pub", 0)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}

	// JSON view.
	jsonRR := httptest.NewRecorder()
	srv.handleSharePublic(jsonRR, httptest.NewRequest(http.MethodGet, "/api/share/"+link.Token, nil))
	if jsonRR.Code != http.StatusOK {
		t.Fatalf("public json status = %d, want 200; body=%s", jsonRR.Code, jsonRR.Body)
	}
	var payload struct {
		Session  *db.Session  `json:"session"`
		Messages []db.Message `json:"messages"`
		Parts    []db.Part    `json:"parts"`
		ReadOnly bool         `json:"readOnly"`
	}
	if err := json.Unmarshal(jsonRR.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal public payload: %v", err)
	}
	if !payload.ReadOnly {
		t.Error("expected readOnly=true")
	}
	if payload.Session == nil || payload.Session.ID != "ses_pub" {
		t.Errorf("unexpected session in payload: %+v", payload.Session)
	}
	if len(payload.Parts) != 1 {
		t.Errorf("expected 1 part, got %d", len(payload.Parts))
	}

	// Markdown view.
	mdRR := httptest.NewRecorder()
	srv.handleSharePublic(mdRR, httptest.NewRequest(http.MethodGet, "/api/share/"+link.Token+"/export.md", nil))
	if mdRR.Code != http.StatusOK {
		t.Fatalf("public md status = %d, want 200", mdRR.Code)
	}
	if !strings.Contains(mdRR.Body.String(), "hello world") {
		t.Errorf("public markdown missing content:\n%s", mdRR.Body)
	}
}

func TestPublicShareViewRevokedReturns404(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_rev")

	link, _ := srv.stateDB.CreateShareLink("fake", "ses_rev", 0)
	if _, err := srv.stateDB.RevokeShareLink("fake", "ses_rev", link.Token); err != nil {
		t.Fatalf("RevokeShareLink: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.handleSharePublic(rr, httptest.NewRequest(http.MethodGet, "/api/share/"+link.Token, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for revoked token", rr.Code)
	}
}

func TestPublicShareViewUnknownTokenReturns404(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_x")

	rr := httptest.NewRecorder()
	srv.handleSharePublic(rr, httptest.NewRequest(http.MethodGet, "/api/share/totallybogustoken", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown token", rr.Code)
	}
}

func TestPublicShareConfiguredBaseURL(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_base")
	srv.WithPublicBaseURL("https://ocman.example.com/")

	req := httptest.NewRequest(http.MethodPost, "/api/session/ses_base/share", nil)
	req.Host = "ignored.local"
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	var view shareLinkView
	_ = json.Unmarshal(rr.Body.Bytes(), &view)
	if !strings.HasPrefix(view.URL, "https://ocman.example.com/share/") {
		t.Errorf("URL %q did not use configured base URL", view.URL)
	}
	if strings.Contains(view.URL, "ignored.local") {
		t.Errorf("URL %q should not use request host when base URL configured", view.URL)
	}
}
