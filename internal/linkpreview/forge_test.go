package linkpreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// forgeAPI fakes a GitHub/Forgejo API. acme/priv is visible only to
// tok-alice and tok-owner; anonymous and other tokens see a 404.
type forgeAPI struct {
	mu    sync.Mutex
	calls []string // token path
}

func (f *forgeAPI) serve(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	f.mu.Lock()
	f.calls = append(f.calls, tok+" "+path)
	f.mu.Unlock()
	if tok == "tok-bad" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.HasPrefix(path, "/repos/acme/priv") && tok != "tok-alice" && tok != "tok-owner" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body := map[string]any{
		"/repos/acme/pub":                     map[string]any{"private": false, "visibility": "public"},
		"/repos/acme/priv":                    map[string]any{"private": true},
		"/repos/acme/internal":                map[string]any{"private": false, "internal": true},
		"/repos/acme/pub/pulls/1":             map[string]any{"title": "Add x", "state": "closed", "merged": true, "user": map[string]any{"login": "ann"}, "updated_at": "2026-01-02T03:04:05Z"},
		"/repos/acme/priv/pulls/2":            map[string]any{"title": "Secret plan", "state": "open", "user": map[string]any{"login": "ann"}},
		"/repos/acme/pub/issues/3":            map[string]any{"title": "Bug", "state": "closed"},
		"/repos/acme/pub/commits/abc1234":     map[string]any{"commit": map[string]any{"message": "fix: y\n\nbody", "author": map[string]any{"name": "Ann", "date": "2026-01-02T03:04:05Z"}}},
		"/repos/acme/pub/git/commits/abc1234": map[string]any{"author": map[string]any{"login": "ann"}, "commit": map[string]any{"message": "gitea commit"}},
		"/user":                               map[string]any{"login": "alice"},
	}[path]
	if body == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *forgeAPI) fetched(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func newForgeHarness(t *testing.T, gitea, connectable bool) (*Service, *forgeAPI, *fakeTokens) {
	api := &forgeAPI{}
	srv := httptest.NewTLSServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	tokens := &fakeTokens{grants: map[string]string{"alice/github.com": "tok-alice"}}
	f := Forge{ID: "github", Host: "github.com", APIBase: srv.URL, Gitea: gitea, OwnerToken: "tok-owner", Connectable: connectable}
	return New(tokens, srv.Client(), f), api, tokens
}

func forgeResolve(ctx context.Context, svc *Service, viewer, text string) []Preview {
	return svc.Resolve(ctx, viewer, "owner", svc.Discover(text, nil))
}

func TestForgeParseURL(t *testing.T) {
	text := "https://github.com/acme/pub/pull/1 https://github.com/acme/pub/pull/1/files " + // dup
		"https://github.com/acme/pub/issues/3. https://github.com/acme/pub/commit/abc1234 " +
		"http://github.com/acme/pub/pull/9 https://github.com.evil.com/acme/pub/pull/9 " +
		"https://evil.com/acme/pub/pull/9 https://github.com/../user/pull/9 https://github.com/a/..%2Fuser/pull/9 " +
		"https://github.com/acme/pub/pull/9x https://github.com/acme/pub/pulls/9 https://tok@github.com/acme/pub/pull/9 " +
		"https://github.com/acme/pub/commit/zz12345 https://github.com/acme/pub/pull/0"
	var got []string
	for _, r := range Discover(text, []Resolver{Forge{ID: "github", Host: "github.com"}}, nil) {
		got = append(got, r.Kind+" "+r.ID+" "+r.URL)
	}
	want := []string{
		"pr acme/pub#1 https://github.com/acme/pub/pull/1",
		"issue acme/pub#3 https://github.com/acme/pub/issues/3",
		"commit acme/pub@abc1234 https://github.com/acme/pub/commit/abc1234",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("refs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	fj := Forge{ID: "forgejo:code.example.com", Host: "code.example.com", Gitea: true}
	refs := Discover("https://code.example.com/a/b/pulls/4 https://code.example.com/a/b/pull/5 https://github.com/a/b/pull/6", []Resolver{fj}, nil)
	if len(refs) != 1 || refs[0].ID != "a/b#4" || refs[0].Provider != fj.ID {
		t.Fatalf("forgejo refs = %+v", refs)
	}
}

func TestForgePublicWithoutLogin(t *testing.T) {
	svc, api, _ := newForgeHarness(t, false, true)
	got := forgeResolve(context.Background(), svc, "", "https://github.com/acme/pub/pull/1 https://github.com/acme/pub/issues/3 https://github.com/acme/pub/commit/abc1234 https://github.com/acme/priv/pull/2")
	if got[0].State != StateOK || got[0].Title != "Add x" || got[0].Status != "Merged" || got[0].Icon != "bi-git" || got[0].Meta[0] != "ann" || got[0].UpdatedAt.IsZero() {
		t.Fatalf("public PR = %+v", got[0])
	}
	if got[1].Status != "Closed" || got[1].Icon != "bi-check-circle" {
		t.Fatalf("issue = %+v", got[1])
	}
	if got[2].Title != "fix: y" || got[2].Status != "Commit" || strings.Join(got[2].Meta, ",") != "Ann,abc1234" {
		t.Fatalf("commit = %+v", got[2])
	}
	if got[3].State != StateConnect || got[3].Title != "" {
		t.Fatalf("private PR without grant = %+v", got[3])
	}
	if api.fetched("/repos/acme/priv/pulls") {
		t.Fatal("private resource was read with the owner token for a public-only request")
	}
}

func TestForgePrivateOnlyForConsentingViewer(t *testing.T) {
	svc, api, _ := newForgeHarness(t, false, true)
	link := "https://github.com/acme/priv/pull/2"
	if p := forgeResolve(context.Background(), svc, "alice", link)[0]; p.State != StateOK || p.Title != "Secret plan" {
		t.Fatalf("alice = %+v", p)
	}
	if !api.fetched("tok-alice /repos/acme/priv/pulls/2") || api.fetched("tok-owner /repos/acme/priv/pulls") {
		t.Fatalf("alice's card must use her own grant: %v", api.calls)
	}
	for _, viewer := range []string{"bob", ""} {
		if p := forgeResolve(context.Background(), svc, viewer, link)[0]; p.State != StateConnect || p.Title != "" {
			t.Fatalf("viewer %q saw %+v", viewer, p)
		}
	}
	// The owner's own direct request may use the owner-wide fallback.
	if p := forgeResolve(WithOwnerAccess(context.Background()), svc, "", link)[0]; p.State != StateOK || p.Title != "Secret plan" {
		t.Fatalf("owner = %+v", p)
	}
	// ...which is cached apart from other viewers.
	if p := forgeResolve(context.Background(), svc, "", link)[0]; p.Title != "" {
		t.Fatalf("owner fallback leaked: %+v", p)
	}
}

func TestForgeNotConnectableAndInternal(t *testing.T) {
	svc, _, _ := newForgeHarness(t, false, false)
	got := forgeResolve(context.Background(), svc, "bob", "https://github.com/acme/priv/pull/2 https://github.com/acme/internal/pull/1")
	for _, p := range got {
		if p.State != StateNotFound || p.Title != "" {
			t.Fatalf("got %+v", p)
		}
	}
}

func TestForgeGiteaCommitAndBadOwnerToken(t *testing.T) {
	svc, _, tokens := newForgeHarness(t, true, true)
	svc.byID["github"] = Forge{ID: "github", Host: "github.com", APIBase: svc.resolvers[0].(Forge).APIBase, Gitea: true, OwnerToken: "tok-bad"}
	p := forgeResolve(context.Background(), svc, "alice", "https://github.com/acme/pub/commit/abc1234")[0]
	if p.Title != "gitea commit" || p.Meta[0] != "ann" {
		t.Fatalf("gitea commit = %+v", p)
	}
	// A rejected owner token is an error, never a revocation of a grant.
	if p := forgeResolve(context.Background(), svc, "bob", "https://github.com/acme/pub/pulls/1")[0]; p.State != StateError {
		t.Fatalf("bad owner token = %+v", p)
	}
	if len(tokens.revoked) != 0 {
		t.Fatalf("revoked %v", tokens.revoked)
	}
}

func TestForgeOAuthProviders(t *testing.T) {
	api := &forgeAPI{}
	srv := httptest.NewTLSServer(http.HandlerFunc(api.serve))
	defer srv.Close()
	gh := GitHubOAuth("id", "secret", srv.URL)
	if gh.ID != "github" || len(gh.Scopes) != 0 || !gh.PKCE || gh.Notice == "" {
		t.Fatalf("github provider = %v", gh)
	}
	if _, err := gh.DecodeToken(map[string]any{"error": "bad_refresh_token"}); !errors.Is(err, previewauth.ErrRevoked) {
		t.Fatalf("bad_refresh_token = %v", err)
	}
	if f, err := gh.DecodeToken(map[string]any{"access_token": "x"}); err != nil || f["access_token"] != "x" {
		t.Fatalf("decode = %v %v", f, err)
	}
	grants, err := gh.Identify(context.Background(), srv.Client(), previewauth.Token{AccessToken: "tok-alice"})
	if err != nil || len(grants) != 1 || grants[0].WorkspaceID != "github.com" || grants[0].AccountName != "alice" {
		t.Fatalf("identify = %+v %v", grants, err)
	}
	if _, err := gh.Identify(context.Background(), srv.Client(), previewauth.Token{AccessToken: "tok-bad"}); !errors.Is(err, previewauth.ErrExchange) {
		t.Fatalf("identify bad token = %v", err)
	}
	fj := ForgejoOAuth("code.example.com", "id", "secret")
	if fj.ID != "forgejo:code.example.com" || fj.AuthURL != "https://code.example.com/login/oauth/authorize" ||
		fj.TokenURL != "https://code.example.com/login/oauth/access_token" || !strings.Contains(fj.Notice, "no granular scopes") {
		t.Fatalf("forgejo provider = %+v", fj)
	}
}
