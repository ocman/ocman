package linkpreview

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/previewauth"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

// linearAPI fakes Linear: each access token belongs to one organization.
type linearAPI struct {
	mu     sync.Mutex
	orgs   map[string]string            // token -> org urlKey
	issues map[string]map[string]string // urlKey -> identifier -> title
	fail   map[string]string            // token -> "forbidden" | "auth400" | "ratelimited"
	calls  []string

	// OAuth state.
	challenge string
	refresh   map[string]bool // live refresh tokens
	refreshN  int
	tokenFail int // next N refreshes answer 500
}

func (f *linearAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/oauth/token" {
		f.token(w, r)
		return
	}
	if r.URL.Path == "/oauth/revoke" {
		_ = r.ParseForm()
		f.calls = append(f.calls, "revoke "+r.PostForm.Get("token"))
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.calls = append(f.calls, tok)
	gqlErr := func(status int, code string) {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"errors":[{"message":"nope","extensions":{"code":%q}}]}`, code)
	}
	key, ok := f.orgs[tok]
	switch {
	case !ok:
		gqlErr(http.StatusBadRequest, "AUTHENTICATION_ERROR")
		return
	case f.fail[tok] == "forbidden":
		gqlErr(http.StatusOK, "FORBIDDEN")
		return
	case f.fail[tok] == "ratelimited":
		gqlErr(http.StatusBadRequest, "RATELIMITED")
		return
	}
	var body struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	org := map[string]any{"id": "org-" + key, "name": strings.ToUpper(key), "urlKey": key}
	if !strings.Contains(body.Query, "issue(") {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"viewer": map[string]any{"name": "Alice"}, "organization": org}})
		return
	}
	id := body.Variables["id"]
	title, found := f.issues[key][id]
	if !found {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"organization": org, "issue": nil},
			"errors": []any{map[string]any{"message": "Entity not found", "extensions": map[string]any{"code": "INVALID_INPUT"}}}})
		return
	}
	issue := map[string]any{
		"identifier": id, "title": title, "url": "https://linear.app/" + key + "/issue/" + id + "/slug",
		"updatedAt": "2026-09-01T10:00:00.000Z", "state": map[string]any{"name": "In Progress"},
		"team": map[string]any{"key": "ENG", "name": "Engineering"}, "assignee": nil,
	}
	if id == "ENG-1" {
		issue["assignee"] = map[string]any{"displayName": "bob", "name": "Bob B"}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"organization": org, "issue": issue}})
}

// token implements authorization_code (PKCE) and rotating refresh grants.
func (f *linearAPI) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	issue := func() {
		f.refreshN++
		access, rt := fmt.Sprintf("at-%d", f.refreshN), fmt.Sprintf("rt-%d", f.refreshN)
		f.orgs[access], f.refresh[rt] = "acme", true
		// 30s is inside the refresh skew: every use refreshes.
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": rt, "expires_in": 30, "token_type": "Bearer"})
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != "code1" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		issue()
	case "refresh_token":
		if f.tokenFail > 0 {
			f.tokenFail--
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		rt := r.PostForm.Get("refresh_token")
		if !f.refresh[rt] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		delete(f.refresh, rt) // rotation: a refresh token is single use
		issue()
	}
}

func newLinearHarness(t *testing.T) (*linearAPI, *httptest.Server) {
	api := &linearAPI{
		orgs:    map[string]string{"tok-alice": "acme", "tok-bob": "other", "tok-dana": "acme", "tok-rl": "acme"},
		issues:  map[string]map[string]string{"acme": {"ENG-1": "Fix login", "ENG-2": "Ship it"}, "other": {"ENG-1": "Other secret"}},
		fail:    map[string]string{"tok-dana": "forbidden", "tok-rl": "ratelimited"},
		refresh: map[string]bool{},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	return api, srv
}

var linearRule = []IdentifierRule{{Pattern: `\bENG-\d+\b`, Provider: "linear"}}

func linearResolve(svc *Service, viewer, text string) []Preview {
	return svc.Resolve(context.Background(), viewer, "owner", svc.Discover(text, linearRule))
}

func TestLinearParseURL(t *testing.T) {
	text := "https://linear.app/acme/issue/ENG-12/fix-the-thing https://LINEAR.app/Acme/issue/eng-13 " +
		"http://linear.app/acme/issue/ENG-14 https://linear.app.evil.com/acme/issue/ENG-15 https://linear.app/acme/project/ENG-16 " +
		"https://linear.app/acme/issue/ENG-0 https://linear.app:444/acme/issue/ENG-17 https://linear.app/../issue/ENG-18"
	var got []string
	for _, r := range Discover(text, []Resolver{Linear{}}, nil) {
		got = append(got, r.Kind+" "+r.ID+" "+r.URL)
	}
	want := "issue ENG-12 https://linear.app/acme/issue/ENG-12\nissue ENG-13 https://linear.app/acme/issue/ENG-13"
	if strings.Join(got, "\n") != want {
		t.Fatalf("refs:\n%s", strings.Join(got, "\n"))
	}
	if _, ok := (Linear{}).ParseIdentifier("../1"); ok {
		t.Fatal("unsafe identifier accepted")
	}
}

func TestLinearIssueCards(t *testing.T) {
	_, srv := newLinearHarness(t)
	tokens := &fakeTokens{grants: map[string]string{
		"alice/org-acme": "tok-alice", "bob/org-other": "tok-bob", "dana/org-acme": "tok-dana",
		"rl/org-acme": "tok-rl", "eve/org-acme": "tok-revoked",
	}}
	svc := New(tokens, srv.Client(), Linear{APIBase: srv.URL})

	// Connected: link and configured identifier.
	ps := linearResolve(svc, "alice", "https://linear.app/acme/issue/ENG-1/x and ENG-2")
	if p := ps[0]; p.State != StateOK || p.Title != "Fix login" || p.Status != "In Progress" ||
		strings.Join(p.Meta, ",") != "Engineering,bob" || p.URL != "https://linear.app/acme/issue/ENG-1/slug" || p.UpdatedAt.IsZero() {
		t.Fatalf("link = %+v", p)
	}
	if p := ps[1]; p.State != StateOK || p.Title != "Ship it" || strings.Join(p.Meta, ",") != "Engineering,Unassigned" {
		t.Fatalf("identifier = %+v", p)
	}
	if p := linearResolve(svc, "alice", "ENG-404")[0]; p.State != StateNotFound || p.Title != "" {
		t.Fatalf("missing = %+v", p)
	}

	// Disconnected: no grant, no data.
	if p := linearResolve(svc, "mallory", "https://linear.app/acme/issue/ENG-1")[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("mallory = %+v", p)
	}
	// Wrong workspace: bob's grant is for "other", which has its own ENG-1.
	if p := linearResolve(svc, "bob", "https://linear.app/acme/issue/ENG-1")[0]; p.State != StateNotFound || p.Title != "" || p.Meta != nil {
		t.Fatalf("wrong workspace = %+v", p)
	}
	// The same identifier in bob's own workspace is his.
	if p := linearResolve(svc, "bob", "https://linear.app/other/issue/ENG-1")[0]; p.State != StateOK || p.Title != "Other secret" {
		t.Fatalf("bob own = %+v", p)
	}
	// FORBIDDEN is denied without data; 429-style errors are rate limited.
	if p := linearResolve(svc, "dana", "ENG-1")[0]; p.State != StateDenied || p.Title != "" {
		t.Fatalf("forbidden = %+v", p)
	}
	if p := linearResolve(svc, "rl", "ENG-1")[0]; p.State != StateRateLimited || p.Title != "" {
		t.Fatalf("rate limited = %+v", p)
	}
	// Revoked token (AUTHENTICATION_ERROR on a 400) forgets the grant.
	if p := linearResolve(svc, "eve", "ENG-1")[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("revoked = %+v", p)
	}
	if strings.Join(tokens.revoked, ",") != "eve/org-acme" {
		t.Fatalf("revoked = %v", tokens.revoked)
	}
	// A cached ENG-1 from alice's workspace never answers another
	// workspace's link to the same identifier.
	if p := linearResolve(svc, "alice", "https://linear.app/other/issue/ENG-1")[0]; p.State != StateNotFound || p.Title != "" {
		t.Fatalf("cross-workspace cache = %+v", p)
	}
}

func TestLinearConnectRefreshAndRetry(t *testing.T) {
	api, srv := newLinearHarness(t)
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	m := previewauth.New(db, "https://ocman.test/cb", srv.Client(), LinearOAuth("cid", "", srv.URL))
	svc := New(m, srv.Client(), Linear{APIBase: srv.URL})

	authURL, err := m.Begin(ctx, "alice", "owner", "linear", "/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if u.Host != "linear.app" || q.Get("scope") != "read" || q.Get("code_challenge_method") != "S256" || q.Get("actor") != "user" {
		t.Fatalf("authorize = %s", authURL)
	}
	api.mu.Lock()
	api.challenge = q.Get("code_challenge")
	api.mu.Unlock()
	if _, err := m.Complete(ctx, "alice", q.Get("state"), "code1", ""); err != nil {
		t.Fatal(err)
	}
	st, _ := m.Status(ctx, "alice", "owner")
	if len(st) != 1 || len(st[0].Connections) != 1 || st[0].Connections[0].WorkspaceID != "org-acme" || st[0].Connections[0].AccountName != "Alice" {
		t.Fatalf("status = %+v", st)
	}

	// The stored token is inside the skew: resolving refreshes and rotates.
	if p := linearResolve(svc, "alice", "ENG-1")[0]; p.State != StateOK || p.Title != "Fix login" {
		t.Fatalf("refreshed = %+v", p)
	}
	c, _ := db.PreviewCredential(ctx, "alice", "owner", "linear", "org-acme")
	if c.RefreshToken != "rt-2" || c.AccessToken != "at-2" {
		t.Fatalf("not rotated: %s %s", c.AccessToken, c.RefreshToken)
	}

	// A transient refresh failure is an error that keeps the grant; the
	// retry redeems the same refresh token.
	api.mu.Lock()
	api.tokenFail = 1
	api.mu.Unlock()
	if p := linearResolve(svc, "alice", "ENG-2")[0]; p.State != StateError || p.Title != "" {
		t.Fatalf("transient = %+v", p)
	}
	if p := linearResolve(svc, "alice", "ENG-2")[0]; p.State != StateOK || p.Title != "Ship it" {
		t.Fatalf("retry = %+v", p)
	}

	// A refresh token Linear revoked (invalid_grant) forgets the grant.
	api.mu.Lock()
	api.refresh = map[string]bool{}
	api.mu.Unlock()
	if p := linearResolve(svc, "alice", "ENG-1")[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("revoked refresh = %+v", p)
	}
	if ws, _ := m.Workspaces(ctx, "alice", "owner", "linear"); len(ws) != 0 {
		t.Fatalf("grant kept: %v", ws)
	}
}
