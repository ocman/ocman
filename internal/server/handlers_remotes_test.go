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
