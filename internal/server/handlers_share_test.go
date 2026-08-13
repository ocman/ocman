package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
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

func TestCreateShareLinkRequiresRelay(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_share")

	createReq := httptest.NewRequest(http.MethodPost, "/api/session/ses_share/share", nil)
	createRR := httptest.NewRecorder()
	srv.dispatchSessionSubpath(createRR, createReq)
	if createRR.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, want 503; body=%s", createRR.Code, createRR.Body)
	}
}

func TestShareLinkViewUsesRelayURL(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	link := state.ShareLink{Token: "local-token", RelayURL: "https://relay.example.com", RelayID: "20260813-abc", RelayKey: "secret-key"}
	view := srv.shareLinkView(httptest.NewRequest(http.MethodGet, "http://localhost:8228/api/session/s/share", nil), link)
	if view.URL != "https://relay.example.com/v/20260813-abc#k=secret-key" {
		t.Fatalf("url = %q", view.URL)
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
	if enabled, err := srv.sharingEnabled(); err != nil || enabled {
		t.Fatalf("sharing should be disabled after POST (enabled=%v, err=%v)", enabled, err)
	}

	// Create is now rejected with 403.
	createRR := httptest.NewRecorder()
	srv.dispatchSessionSubpath(createRR, httptest.NewRequest(http.MethodPost, "/api/session/ses_guard/share", nil))
	if createRR.Code != http.StatusForbidden {
		t.Fatalf("create status = %d, want 403 when disabled; body=%s", createRR.Code, createRR.Body)
	}

	// Re-enable still needs a relay; a local URL must not be minted.
	reEnable := httptest.NewRecorder()
	srv.handleSharingSetting(reEnable, httptest.NewRequest(http.MethodPost, "/api/settings/sharing", strings.NewReader(`{"enabled":true}`)))
	create2 := httptest.NewRecorder()
	srv.dispatchSessionSubpath(create2, httptest.NewRequest(http.MethodPost, "/api/session/ses_guard/share", nil))
	if create2.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, want 503 without relay; body=%s", create2.Code, create2.Body)
	}
}

// TestSharingSettingReportsRelay proves the relay configured on the
// command line is visible to the Settings page, including which input
// supplied it.
func TestSharingSettingReportsRelay(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		source     string
		wantURL    string
		wantSource string
	}{
		{
			name:       "flag-configured relay is reported",
			url:        "http://localhost:8231",
			source:     "flag",
			wantURL:    "http://localhost:8231",
			wantSource: "flag",
		},
		{
			name:       "trailing slash is normalised",
			url:        "https://share.example.com/",
			source:     "env",
			wantURL:    "https://share.example.com",
			wantSource: "env",
		},
		{
			name: "no relay reports empty url and source",
		},
		{
			// A source without a URL would render a provenance for a
			// relay that does not exist.
			name:   "source is cleared when the url is empty",
			url:    "",
			source: "builtin",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newSessionsTestServer(t)
			srv.WithRelay(tc.url, tc.source)

			rr := httptest.NewRecorder()
			srv.handleSharingSetting(rr, httptest.NewRequest(http.MethodGet, "/api/settings/sharing", nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			var got sharingSettingView
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.RelayURL != tc.wantURL {
				t.Fatalf("relayUrl = %q, want %q", got.RelayURL, tc.wantURL)
			}
			if got.RelaySource != tc.wantSource {
				t.Fatalf("relaySource = %q, want %q", got.RelaySource, tc.wantSource)
			}
			if !got.Enabled {
				t.Fatal("sharing should still default to enabled")
			}
		})
	}
}

// TestSharingSettingPostKeepsRelay proves a toggle response carries the
// relay too, so the UI does not blank the field after saving.
func TestSharingSettingPostKeepsRelay(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	srv.WithRelay("http://localhost:8231", "flag")

	rr := httptest.NewRecorder()
	srv.handleSharingSetting(rr, httptest.NewRequest(http.MethodPost, "/api/settings/sharing", strings.NewReader(`{"enabled":false}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var got sharingSettingView
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Enabled {
		t.Fatal("enabled should be false after the POST")
	}
	if got.RelayURL != "http://localhost:8231" || got.RelaySource != "flag" {
		t.Fatalf("relay dropped from the POST response: %+v", got)
	}
}

// TestSharingFailsClosedOnStateError proves the sharing gate fails
// CLOSED. A transient state-DB read error must not re-enable minting
// public, unauthenticated links after an operator disabled sharing.
func TestSharingFailsClosedOnStateError(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_failclosed")

	// Break the setting read the same way a broken/locked DB would.
	if err := srv.stateDB.Close(); err != nil {
		t.Fatalf("close state db: %v", err)
	}

	if _, err := srv.sharingEnabled(); err == nil {
		t.Fatal("sharingEnabled must report the read error, not assume enabled")
	}

	createRR := httptest.NewRecorder()
	srv.dispatchSessionSubpath(createRR, httptest.NewRequest(http.MethodPost, "/api/session/ses_failclosed/share", nil))
	if createRR.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, want 503 when the sharing setting can't be read; body=%s",
			createRR.Code, createRR.Body)
	}

	// The read-only status endpoint reports unavailable rather than
	// claiming sharing is on.
	getRR := httptest.NewRecorder()
	srv.handleSharingSetting(getRR, httptest.NewRequest(http.MethodGet, "/api/settings/sharing", nil))
	if getRR.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET status = %d, want 503 when the setting can't be read; body=%s", getRR.Code, getRR.Body)
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
		if v.SessionID == "" || v.Platform != "fake" {
			t.Errorf("incomplete global view: %+v", v)
		}
	}
}
