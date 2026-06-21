package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/remote"
)

func withManager(t *testing.T, srv *Server) *remote.Manager {
	t.Helper()
	mgr := remote.NewManager(srv.Registry(), srv.HostRouter(), srv.stateDB, string(opencodeplatform.PlatformID))
	srv.SetRemoteManager(mgr)
	t.Cleanup(mgr.Stop)
	return mgr
}

func TestHandleRemotes_NoManagerErrors(t *testing.T) {
	srv := testServer(t) // no manager

	// POST without a manager is a 503.
	post := httptest.NewRequest(http.MethodPost, "/api/remotes", bytes.NewBufferString(`{}`))
	pr := httptest.NewRecorder()
	srv.handleRemotes(pr, post)
	if pr.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST no-manager = %d, want 503", pr.Code)
	}

	// Any /api/remotes/{id} call is a 503.
	idreq := httptest.NewRequest(http.MethodDelete, "/api/remotes/1", nil)
	ir := httptest.NewRecorder()
	srv.handleRemoteByID(ir, idreq)
	if ir.Code != http.StatusServiceUnavailable {
		t.Fatalf("byID no-manager = %d, want 503", ir.Code)
	}
}

func TestHandleRemotes_MethodAndValidationErrors(t *testing.T) {
	srv := testServer(t)
	withManager(t, srv)

	// Unsupported method on the collection.
	put := httptest.NewRequest(http.MethodPut, "/api/remotes", nil)
	r1 := httptest.NewRecorder()
	srv.handleRemotes(r1, put)
	if r1.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /api/remotes = %d, want 405", r1.Code)
	}

	// Add with missing address/token -> 400.
	bad := httptest.NewRequest(http.MethodPost, "/api/remotes", bytes.NewBufferString(`{"address":""}`))
	bad.Header.Set("Content-Type", "application/json")
	r2 := httptest.NewRecorder()
	srv.handleRemotes(r2, bad)
	if r2.Code != http.StatusBadRequest {
		t.Errorf("add missing fields = %d, want 400", r2.Code)
	}

	// Invalid id.
	badid := httptest.NewRequest(http.MethodDelete, "/api/remotes/notanint", nil)
	r3 := httptest.NewRecorder()
	srv.handleRemoteByID(r3, badid)
	if r3.Code != http.StatusBadRequest {
		t.Errorf("invalid id = %d, want 400", r3.Code)
	}

	// Unknown subaction.
	unknown := httptest.NewRequest(http.MethodGet, "/api/remotes/1/bogus", nil)
	r4 := httptest.NewRecorder()
	srv.handleRemoteByID(r4, unknown)
	if r4.Code != http.StatusNotFound {
		t.Errorf("unknown action = %d, want 404", r4.Code)
	}

	// reconnect with the wrong method.
	rc := httptest.NewRequest(http.MethodGet, "/api/remotes/1/reconnect", nil)
	r5 := httptest.NewRecorder()
	srv.handleRemoteByID(r5, rc)
	if r5.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET reconnect = %d, want 405", r5.Code)
	}

	// Bare id with an unsupported method.
	patch := httptest.NewRequest(http.MethodPatch, "/api/remotes/1", nil)
	r6 := httptest.NewRecorder()
	srv.handleRemoteByID(r6, patch)
	if r6.Code != http.StatusMethodNotAllowed {
		t.Errorf("PATCH /api/remotes/1 = %d, want 405", r6.Code)
	}
}

func TestHandleRemoteUpdate_Validation(t *testing.T) {
	srv := testServer(t)
	withManager(t, srv)
	id, _ := srv.stateDB.AddRemote("a:1", "t", "N")

	// Missing address -> 400.
	miss := httptest.NewRequest(http.MethodPut, "/api/remotes/"+strconv.FormatInt(id, 10),
		bytes.NewBufferString(`{"displayName":"x","address":"","enabled":true}`))
	miss.Header.Set("Content-Type", "application/json")
	r1 := httptest.NewRecorder()
	srv.handleRemoteByID(r1, miss)
	if r1.Code != http.StatusBadRequest {
		t.Fatalf("update missing address = %d, want 400", r1.Code)
	}

	// Replace token (exercises the token!=nil branch).
	tok := `{"displayName":"x","address":"a:2","enabled":true,"token":"newtok"}`
	rep := httptest.NewRequest(http.MethodPut, "/api/remotes/"+strconv.FormatInt(id, 10),
		bytes.NewBufferString(tok))
	rep.Header.Set("Content-Type", "application/json")
	r2 := httptest.NewRecorder()
	srv.handleRemoteByID(r2, rep)
	if r2.Code != http.StatusOK {
		t.Fatalf("update with token = %d body=%s", r2.Code, r2.Body.String())
	}
	if got, _ := srv.stateDB.RemoteToken(id); got != "newtok" {
		t.Errorf("token not replaced: %q", got)
	}
}

func TestHandleRemotes_EmptyListWhenNoManager(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/remotes", nil)
	rr := httptest.NewRecorder()
	srv.handleRemotes(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if got := rr.Body.String(); got != "[]\n" {
		t.Fatalf("expected empty list, got %q", got)
	}
}

func TestHandleRemotes_AddListUpdateDelete(t *testing.T) {
	srv := testServer(t)
	withManager(t, srv)

	// Add — uses an unreachable address so the dial just fails in the
	// background; the row is still persisted (AD-10b).
	body := `{"address":"127.0.0.1:59999","token":"secret","displayName":"Box"}`
	req := httptest.NewRequest(http.MethodPost, "/api/remotes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleRemotes(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("add status %d body=%s", rr.Code, rr.Body.String())
	}
	var added remote.RemoteStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if added.LocalID == 0 || added.DisplayName != "Box" {
		t.Fatalf("unexpected add result: %+v", added)
	}
	// Token must never be echoed back.
	if bytes.Contains(rr.Body.Bytes(), []byte("secret")) {
		t.Fatal("token leaked in add response")
	}

	// List
	lreq := httptest.NewRequest(http.MethodGet, "/api/remotes", nil)
	lrr := httptest.NewRecorder()
	srv.handleRemotes(lrr, lreq)
	var list []remote.RemoteStatus
	if err := json.Unmarshal(lrr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Address != "127.0.0.1:59999" {
		t.Fatalf("unexpected list: %+v", list)
	}

	id := strconv.FormatInt(added.LocalID, 10)

	// Update (rename + disable)
	ubody := `{"address":"127.0.0.1:59999","displayName":"Renamed","enabled":false}`
	ureq := httptest.NewRequest(http.MethodPut, "/api/remotes/"+id, bytes.NewBufferString(ubody))
	ureq.Header.Set("Content-Type", "application/json")
	urr := httptest.NewRecorder()
	srv.handleRemoteByID(urr, ureq)
	if urr.Code != http.StatusOK {
		t.Fatalf("update status %d body=%s", urr.Code, urr.Body.String())
	}

	got, err := srv.stateDB.GetRemote(added.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Renamed" || got.Enabled {
		t.Fatalf("update not persisted: %+v", got)
	}

	// Reconnect (no-op while disabled, should still 200)
	rcreq := httptest.NewRequest(http.MethodPost, "/api/remotes/"+id+"/reconnect", nil)
	rcrr := httptest.NewRecorder()
	srv.handleRemoteByID(rcrr, rcreq)
	if rcrr.Code != http.StatusOK {
		t.Fatalf("reconnect status %d", rcrr.Code)
	}

	// Delete
	dreq := httptest.NewRequest(http.MethodDelete, "/api/remotes/"+id, nil)
	drr := httptest.NewRecorder()
	srv.handleRemoteByID(drr, dreq)
	if drr.Code != http.StatusOK {
		t.Fatalf("delete status %d", drr.Code)
	}
	list2, _ := srv.remotes.List()
	if len(list2) != 0 {
		t.Fatalf("expected empty after delete, got %+v", list2)
	}
}
