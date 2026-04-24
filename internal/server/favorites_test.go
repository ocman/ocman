package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callFavorites invokes handleFavoritesRoot directly (bypassing the
// mux) with the given method, query, and body. Returns the recorder so
// callers can assert on status + body.
func callFavorites(t *testing.T, srv *Server, method, query, body string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/favorites"
	if query != "" {
		url += "?" + query
	}
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, url, nil)
	}
	w := httptest.NewRecorder()
	srv.handleFavoritesRoot(w, r)
	return w
}

func TestFavorites_AddListRemove_RoundTrip(t *testing.T) {
	srv := testServer(t)

	// Initially empty.
	w := callFavorites(t, srv, http.MethodGet, "platform=opencode", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var initial []map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if len(initial) != 0 {
		t.Errorf("expected empty favorites, got %d", len(initial))
	}

	// Add.
	w = callFavorites(t, srv, http.MethodPost, "",
		`{"platform":"opencode","provider":"anthropic","model":"claude-opus-4"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// List.
	w = callFavorites(t, srv, http.MethodGet, "platform=opencode", "")
	var favs []map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &favs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(favs) != 1 {
		t.Fatalf("expected 1 favorite, got %d", len(favs))
	}
	if favs[0]["provider"] != "anthropic" || favs[0]["model"] != "claude-opus-4" {
		t.Errorf("unexpected favorite payload: %+v", favs[0])
	}

	// Remove.
	w = callFavorites(t, srv, http.MethodDelete, "",
		`{"platform":"opencode","provider":"anthropic","model":"claude-opus-4"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Back to empty.
	w = callFavorites(t, srv, http.MethodGet, "platform=opencode", "")
	_ = json.Unmarshal(w.Body.Bytes(), &favs)
	if len(favs) != 0 {
		t.Errorf("expected 0 favorites after DELETE, got %d", len(favs))
	}
}

func TestFavorites_RejectsMissingPlatform(t *testing.T) {
	srv := testServer(t)
	w := callFavorites(t, srv, http.MethodGet, "", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing platform, got %d", w.Code)
	}
}

func TestFavorites_RejectsUnknownPlatform(t *testing.T) {
	srv := testServer(t)
	w := callFavorites(t, srv, http.MethodGet, "platform=martian", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown platform, got %d", w.Code)
	}
}

func TestFavorites_RejectsMissingModel(t *testing.T) {
	srv := testServer(t)
	w := callFavorites(t, srv, http.MethodPost, "",
		`{"platform":"opencode","provider":"anthropic"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing model, got %d", w.Code)
	}
}

// TestFavorites_AddIdempotent verifies that adding the same favorite
// twice is a no-op — matches AddModelFavorite's ON CONFLICT DO NOTHING.
func TestFavorites_AddIdempotent(t *testing.T) {
	srv := testServer(t)
	body := `{"platform":"opencode","provider":"anthropic","model":"claude-opus-4"}`
	for i := 0; i < 3; i++ {
		w := callFavorites(t, srv, http.MethodPost, "", body)
		if w.Code != http.StatusNoContent {
			t.Fatalf("POST #%d expected 204, got %d", i, w.Code)
		}
	}
	w := callFavorites(t, srv, http.MethodGet, "platform=opencode", "")
	var favs []map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &favs)
	if len(favs) != 1 {
		t.Errorf("expected 1 favorite after 3 POSTs, got %d", len(favs))
	}
}

// TestFavorites_MethodNotAllowed covers PUT / PATCH (not handled).
func TestFavorites_MethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	w := callFavorites(t, srv, http.MethodPut, "platform=opencode", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT expected 405, got %d", w.Code)
	}
}
