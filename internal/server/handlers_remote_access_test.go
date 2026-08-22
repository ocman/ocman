package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleRemoteAccess_ReportsIdentityNoToken(t *testing.T) {
	srv := testServer(t)
	srv.WithRemoteAccess("inst123", "0.0.0.0:8230", true, false)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/remote-access", nil)
	rr := httptest.NewRecorder()
	srv.handleRemoteAccess(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got remoteAccessStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.InstanceID != "inst123" {
		t.Errorf("instanceId: got %q", got.InstanceID)
	}
	if !got.Listening || got.ListenAddr != "0.0.0.0:8230" {
		t.Errorf("listen status: %+v", got)
	}
	if got.Transport != "trusted-overlay" {
		t.Errorf("transport: got %q, want trusted-overlay", got.Transport)
	}
	if !got.TokenSet {
		t.Error("expected tokenSet true (stateDB seeds identity)")
	}
	// The plaintext token must never appear in the status response.
	if got.InstanceID == "" {
		t.Error("instance id missing")
	}
}

func TestHandleRevealRemoteToken_ReturnsPlaintext(t *testing.T) {
	srv := testServer(t)

	// Establish the stored identity so reveal returns the same token.
	ident, err := srv.stateDB.InstanceIdentity(t.Context())
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/remote-access/reveal-token", nil)
	rr := httptest.NewRecorder()
	srv.handleRevealRemoteToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["token"] != ident.RemoteToken || got["token"] == "" {
		t.Errorf("reveal token mismatch: got %q want %q", got["token"], ident.RemoteToken)
	}
}

func TestRevealRemoteTokenRouteRejectsNonLoopback(t *testing.T) {
	srv := testServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/remote-access/reveal-token", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}
