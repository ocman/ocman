package previewauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func openState(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSafeReturnPath(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "/",
		"/settings?a=1":        "/settings?a=1",
		"//evil.example":       "/",
		`/\evil.example`:       "/",
		"https://evil.example": "/",
		"settings":             "/",
		"/x\r\nSet-Cookie:":    "/",
	} {
		if got := SafeReturnPath(in); got != want {
			t.Errorf("SafeReturnPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAccessTokenRefreshIsSynchronizedAndRotates(t *testing.T) {
	var refreshes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if u, p, ok := r.BasicAuth(); !ok || u != "cid" || p != "sec" || r.PostForm.Get("client_secret") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(&refreshes, 1)
		time.Sleep(50 * time.Millisecond)
		if r.PostForm.Get("refresh_token") == "dead" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600})
	}))
	defer srv.Close()
	db := openState(t)
	ctx := context.Background()
	m := New(db, "http://localhost/cb", nil, Provider{ID: "p", TokenURL: srv.URL, ClientID: "cid", ClientSecret: "sec", BasicAuth: true})
	put := func(ws, refresh string, exp time.Time) {
		if err := db.PutPreviewCredential(ctx, state.PreviewCredential{ViewerID: "v", OwnerID: "o", Provider: "p", WorkspaceID: ws, AccessToken: "old", RefreshToken: refresh, ExpiresAt: exp}); err != nil {
			t.Fatal(err)
		}
	}
	put("w", "r1", time.Now().Add(-time.Minute))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := m.AccessToken(ctx, "v", "o", "p", "w"); err != nil || tok != "new-access" {
				t.Errorf("AccessToken = %q, %v", tok, err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&refreshes); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
	c, err := db.PreviewCredential(ctx, "v", "o", "p", "w")
	if err != nil || c.RefreshToken != "new-refresh" {
		t.Fatalf("rotated refresh = %q, %v", c.RefreshToken, err)
	}
	// A fresh token is served without a refresh; another viewer gets nothing.
	if tok, _ := m.AccessToken(ctx, "v", "o", "p", "w"); tok != "new-access" || atomic.LoadInt32(&refreshes) != 1 {
		t.Fatal("fresh token refreshed again")
	}
	if _, err := m.AccessToken(ctx, "other", "o", "p", "w"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("other viewer = %v", err)
	}
	if _, err := m.AccessToken(ctx, "v", "other-owner", "p", "w"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("other owner = %v", err)
	}

	// A revoked refresh token deletes the grant.
	put("dead", "dead", time.Now().Add(-time.Minute))
	if _, err := m.AccessToken(ctx, "v", "o", "p", "dead"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked = %v", err)
	}
	if _, err := db.PreviewCredential(ctx, "v", "o", "p", "dead"); !errors.Is(err, state.ErrPreviewNotFound) {
		t.Fatalf("revoked grant kept: %v", err)
	}

	// Expired without refresh token reports expired in status and on use.
	put("stale", "", time.Now().Add(-time.Minute))
	if _, err := m.AccessToken(ctx, "v", "o", "p", "stale"); !errors.Is(err, ErrExpired) {
		t.Fatalf("stale = %v", err)
	}
	st, err := m.Status(ctx, "v", "o")
	if err != nil || len(st) != 1 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	states := map[string]string{}
	for _, c := range st[0].Connections {
		states[c.WorkspaceID] = c.State
	}
	if states["stale"] != "expired" || states["w"] != "connected" {
		t.Fatalf("states = %v", states)
	}
}

func TestCompleteHandlesDenialAndUnknownState(t *testing.T) {
	db := openState(t)
	ctx := context.Background()
	m := New(db, "http://localhost/cb", nil, Provider{ID: "p", AuthURL: "https://auth.example/authorize", TokenURL: "http://127.0.0.1:1/token", ClientID: "cid"})
	if _, err := m.Begin(ctx, "v", "o", "nope", "/"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown provider = %v", err)
	}
	authURL, err := m.Begin(ctx, "v", "o", "p", "/back")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if u.Query().Get("code_challenge") != "" {
		t.Fatal("PKCE sent for a provider without PKCE")
	}
	st := u.Query().Get("state")
	ret, err := m.Complete(ctx, "v", st, "", "access_denied")
	if ret != "/back" || !errors.Is(err, ErrExchange) {
		t.Fatalf("denied = %q, %v", ret, err)
	}
	if _, err := m.Complete(ctx, "v", st, "code", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reused state = %v", err)
	}
	other, _ := m.Begin(ctx, "v", "o", "p", "/")
	ou, _ := url.Parse(other)
	if _, err := m.Complete(ctx, "intruder", ou.Query().Get("state"), "code", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("other viewer's state = %v", err)
	}
	if err := m.Disconnect(ctx, "v", "o", "nope", ""); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("disconnect unknown = %v", err)
	}
	if got := (Provider{ID: "p", ClientSecret: "sec"}).String(); got != "Provider(p)" {
		t.Fatalf("String = %q", got)
	}
}
