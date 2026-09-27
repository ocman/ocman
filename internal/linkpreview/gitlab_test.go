package linkpreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/previewauth"
	"github.com/NoUseFreak/ocman/internal/state"
)

// gitlabAPI fakes one GitLab instance (API v4 + OAuth endpoints).
type gitlabAPI struct {
	mu       sync.Mutex
	tokens   map[string]bool   // valid access tokens
	projects map[string]string // encoded project path -> visibility
	calls    []string          // escaped request paths
	auths    []string          // Authorization headers seen
	status   int               // forced status for resource calls
	refresh  map[string]bool
	n        int
	revoked  []string
	form     url.Values
}

func (f *gitlabAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/oauth/token":
		f.token(w, r)
		return
	case "/oauth/revoke":
		_ = r.ParseForm()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		return
	}
	f.calls = append(f.calls, r.URL.EscapedPath())
	auth := r.Header.Get("Authorization")
	f.auths = append(f.auths, auth)
	viewer := auth != ""
	if viewer && !f.tokens[strings.TrimPrefix(auth, "Bearer ")] {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/api/v4/")
	if rest == "user" {
		fmt.Fprint(w, `{"username":"alice"}`)
		return
	}
	parts := strings.Split(rest, "/")
	if !ok || len(parts) < 2 || parts[0] != "projects" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	vis, found := f.projects[parts[1]]
	if !found || (!viewer && vis != "public") {
		w.WriteHeader(http.StatusNotFound) // GitLab hides non-public projects
		return
	}
	if f.status != 0 && len(parts) > 2 {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(f.status)
		return
	}
	switch strings.Join(parts[2:], "/") {
	case "":
		fmt.Fprintf(w, `{"visibility":%q}`, vis)
	case "merge_requests/7":
		fmt.Fprint(w, `{"title":"Add feature","state":"merged","updated_at":"2026-09-01T10:00:00Z","author":{"username":"bob"},"web_url":"https://evil.example.com"}`)
	case "issues/3":
		fmt.Fprint(w, `{"title":"Crash on start","state":"opened","updated_at":"2026-09-01T10:00:00Z","author":{"username":"carol"}}`)
	case "repository/commits/abcdef1234":
		fmt.Fprint(w, `{"title":"Fix typo","author_name":"Dave","committed_date":"2026-09-01T10:00:00Z"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// token implements authorization_code (with PKCE) and rotating refresh.
func (f *gitlabAPI) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.form = r.PostForm
	issue := func() {
		f.n++
		access, rt := fmt.Sprintf("at-%d", f.n), fmt.Sprintf("rt-%d", f.n)
		f.tokens[access], f.refresh[rt] = true, true
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_in":30,"token_type":"Bearer"}`, access, rt)
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if r.PostForm.Get("code") != "code1" || r.PostForm.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		issue()
	case "refresh_token":
		if !f.refresh[r.PostForm.Get("refresh_token")] {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		delete(f.refresh, r.PostForm.Get("refresh_token"))
		issue()
	}
}

func newGitLabServer(t *testing.T, tokens ...string) (*gitlabAPI, *httptest.Server) {
	f := &gitlabAPI{
		tokens:   map[string]bool{},
		projects: map[string]string{"grp%2Fsub%2Fapp": "public", "grp%2Fsecret": "private", "grp%2Finternal": "internal"},
		refresh:  map[string]bool{},
	}
	for _, tok := range tokens {
		f.tokens[tok] = true
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

// allowLoopback lets the dial guard reach httptest servers.
func allowLoopback(t *testing.T) {
	gitlabAllow = func(netip.Addr) bool { return true }
	t.Cleanup(func() { gitlabAllow = nil })
}

func gitlabResolve(svc *Service, viewer, text string) []Preview {
	return svc.Resolve(context.Background(), viewer, "owner", svc.Discover(text, nil))
}

func TestGitLabParseURL(t *testing.T) {
	g := GitLab{Host: "gitlab.com"}
	text := "https://gitlab.com/grp/sub/app/-/merge_requests/7/diffs https://GitLab.com/grp/app/-/issues/3 " +
		"https://gitlab.com/grp/app/-/commit/ABCDEF12 http://gitlab.com/grp/app/-/issues/1 " +
		"https://gitlab.com.evil.com/grp/app/-/issues/1 https://gitlab.com:8443/grp/app/-/issues/1 " +
		"https://gitlab.com/app/-/issues/1 https://gitlab.com/grp/app/issues/1 https://gitlab.com/grp/../app/-/issues/1 " +
		"https://gitlab.com/grp/app/-/issues/0 https://gitlab.com/grp/app/-/pipelines/1 https://gitlab.com/grp/app/-/commit/xyz"
	var got []string
	for _, r := range Discover(text, []Resolver{g}, nil) {
		got = append(got, r.Provider+" "+r.Kind+" "+r.ID+" "+r.URL)
	}
	want := "gitlab:gitlab.com pr grp/sub/app!7 https://gitlab.com/grp/sub/app/-/merge_requests/7\n" +
		"gitlab:gitlab.com issue grp/app#3 https://gitlab.com/grp/app/-/issues/3\n" +
		"gitlab:gitlab.com commit grp/app@ABCDEF12 https://gitlab.com/grp/app/-/commit/ABCDEF12"
	if strings.Join(got, "\n") != want {
		t.Fatalf("refs:\n%s", strings.Join(got, "\n"))
	}
	if _, ok := g.ParseIdentifier("grp/app#1"); ok {
		t.Fatal("identifier accepted")
	}
	if _, err := g.Fetch(context.Background(), &API{}, Ref{Kind: "pr", ID: "no-separator"}); !errors.Is(err, ErrUnsafeRequest) {
		t.Fatalf("bad id err = %v", err)
	}
}

func TestGitLabCardsAndInstanceIsolation(t *testing.T) {
	allowLoopback(t)
	com, comSrv := newGitLabServer(t, "tok-com")
	corp, corpSrv := newGitLabServer(t, "tok-corp")
	// One client trusting both test CAs.
	client := comSrv.Client()
	client.Transport.(*http.Transport).TLSClientConfig.RootCAs.AddCert(corpSrv.Certificate())
	// The real Manager: grants are keyed by provider, i.e. by instance.
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, c := range []state.PreviewCredential{
		{ViewerID: "alice", OwnerID: "owner", Provider: "gitlab:gitlab.com", WorkspaceID: "gitlab.com", AccessToken: "tok-com"},
		{ViewerID: "bob", OwnerID: "owner", Provider: "gitlab:code.corp", WorkspaceID: "code.corp", AccessToken: "tok-corp"},
	} {
		if err := db.PutPreviewCredential(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	m := previewauth.New(db, "https://ocman.test/cb", client, GitLabOAuth("gitlab.com", "a", "", "", ""), GitLabOAuth("code.corp", "b", "", "", ""))
	svc := New(m, client,
		GitLab{Host: "gitlab.com", APIBase: comSrv.URL + "/api/v4"},
		GitLab{Host: "code.corp", APIBase: corpSrv.URL + "/api/v4"})

	ps := gitlabResolve(svc, "alice", "https://gitlab.com/grp/secret/-/merge_requests/7 https://gitlab.com/grp/secret/-/issues/3 "+
		"https://gitlab.com/grp/sub/app/-/commit/abcdef1234 https://code.corp/grp/secret/-/issues/3")
	if p := ps[0]; p.State != StateOK || p.Title != "Add feature" || p.Status != "Merged" || p.Meta[0] != "bob" ||
		p.URL != "https://gitlab.com/grp/secret/-/merge_requests/7" || p.UpdatedAt.IsZero() {
		t.Fatalf("mr = %+v", p)
	}
	if p := ps[1]; p.State != StateOK || p.Status != "Open" || p.Icon != "bi-circle" {
		t.Fatalf("issue = %+v", p)
	}
	if p := ps[2]; p.State != StateOK || p.Title != "Fix typo" || strings.Join(p.Meta, ",") != "Dave,abcdef12" {
		t.Fatalf("commit = %+v", p)
	}
	// Alice has no grant on the self-managed instance: its private project
	// asks her to connect, and her gitlab.com token never reaches it.
	if p := ps[3]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("other instance = %+v", p)
	}
	for _, a := range corp.auths {
		if a != "" {
			t.Fatalf("token leaked to self-managed: %q", a)
		}
	}
	if !strings.Contains(strings.Join(com.calls, " "), "/api/v4/projects/grp%2Fsecret/merge_requests/7") {
		t.Fatalf("calls = %v", com.calls)
	}
	// Bob reads the self-managed project with his own grant.
	if p := gitlabResolve(svc, "bob", "https://code.corp/grp/secret/-/issues/3")[0]; p.State != StateOK || p.Title != "Crash on start" {
		t.Fatalf("bob = %+v", p)
	}
	for _, a := range com.auths {
		if a == "Bearer tok-corp" {
			t.Fatal("self-managed token sent to gitlab.com")
		}
	}
}

func TestGitLabVisibilityWithoutLogin(t *testing.T) {
	allowLoopback(t)
	f, srv := newGitLabServer(t)
	svc := New(&fakeTokens{}, srv.Client(), GitLab{Host: "gitlab.com", APIBase: srv.URL + "/api/v4"})
	ps := gitlabResolve(svc, "", "https://gitlab.com/grp/sub/app/-/issues/3 https://gitlab.com/grp/secret/-/issues/3 https://gitlab.com/grp/internal/-/issues/3")
	if p := ps[0]; p.State != StateOK || p.Title != "Crash on start" {
		t.Fatalf("public = %+v", p)
	}
	if ps[1].State != StateConnect || ps[2].State != StateConnect {
		t.Fatalf("private/internal = %+v %+v", ps[1], ps[2])
	}
	for _, c := range f.calls {
		if strings.Contains(c, "secret/") || strings.Contains(c, "internal/") {
			t.Fatalf("non-public resource read: %s", c)
		}
	}
}

func TestGitLabRateLimitedAndRedirect(t *testing.T) {
	allowLoopback(t)
	f, srv := newGitLabServer(t, "tok")
	svc := New(&fakeTokens{grants: map[string]string{"alice/gitlab.com": "tok"}}, srv.Client(), GitLab{Host: "gitlab.com", APIBase: srv.URL + "/api/v4"})
	f.status = http.StatusTooManyRequests
	if p := gitlabResolve(svc, "alice", "https://gitlab.com/grp/secret/-/issues/3")[0]; p.State != StateRateLimited {
		t.Fatalf("429 = %+v", p)
	}
	// A redirect (e.g. to an internal address) is never followed.
	f.status = http.StatusFound
	if p := gitlabResolve(svc, "alice", "https://gitlab.com/grp/sub/app/-/issues/3")[0]; p.State == StateOK {
		t.Fatalf("redirect = %+v", p)
	}
}

func TestGitLabDialGuard(t *testing.T) {
	for _, c := range []struct {
		ip      string
		private bool
		want    bool
	}{
		{"172.65.251.78", false, true},
		{"127.0.0.1", true, false},
		{"::1", true, false},
		{"169.254.169.254", true, false},
		{"fe80::1", true, false},
		{"0.0.0.0", true, false},
		{"::ffff:127.0.0.1", true, false},
		{"10.0.0.5", false, false},
		{"10.0.0.5", true, true},
		{"100.100.1.1", false, false},
		{"100.100.1.1", true, true},
		{"fd00::1", false, false},
	} {
		if got := gitlabAddrOK(netip.MustParseAddr(c.ip), c.private); got != c.want {
			t.Errorf("%s private=%v: %v", c.ip, c.private, got)
		}
	}
	// With the real policy, a host that resolves to loopback (rebinding to
	// the local machine) is never dialed.
	f, srv := newGitLabServer(t, "tok")
	svc := New(&fakeTokens{grants: map[string]string{"alice/code.corp": "tok"}}, srv.Client(), GitLab{Host: "code.corp", APIBase: srv.URL + "/api/v4"})
	if p := gitlabResolve(svc, "alice", "https://code.corp/grp/secret/-/issues/3")[0]; p.State != StateError {
		t.Fatalf("loopback = %+v", p)
	}
	if len(f.calls) != 0 {
		t.Fatalf("loopback dialed: %v", f.calls)
	}
}

func TestGitLabConnectRefreshAndRevoke(t *testing.T) {
	allowLoopback(t)
	f, srv := newGitLabServer(t)
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	m := previewauth.New(db, "https://ocman.test/cb", srv.Client(), GitLabOAuth("code.corp", "cid", "", srv.URL+"/api/v4", srv.URL))
	svc := New(m, srv.Client(), GitLab{Host: "code.corp", APIBase: srv.URL + "/api/v4"})

	authURL, err := m.Begin(ctx, "alice", "owner", "gitlab:code.corp", "/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if u.Path != "/oauth/authorize" || q.Get("scope") != "read_api" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("authorize = %s", authURL)
	}
	if _, err := m.Complete(ctx, "alice", q.Get("state"), "code1", ""); err != nil {
		t.Fatal(err)
	}
	st, _ := m.Status(ctx, "alice", "owner")
	if len(st) != 1 || len(st[0].Connections) != 1 || st[0].Connections[0].AccountName != "alice" || st[0].Connections[0].WorkspaceID != "code.corp" {
		t.Fatalf("status = %+v", st)
	}
	// expires_in 30 is inside the refresh skew: resolving refreshes first.
	if p := gitlabResolve(svc, "alice", "https://code.corp/grp/secret/-/issues/3")[0]; p.State != StateOK {
		t.Fatalf("refreshed = %+v", p)
	}
	c, _ := db.PreviewCredential(ctx, "alice", "owner", "gitlab:code.corp", "code.corp")
	if c.AccessToken != "at-2" || c.RefreshToken != "rt-2" || f.form.Get("client_id") != "cid" {
		t.Fatalf("not refreshed: %s %s %v", c.AccessToken, c.RefreshToken, f.form)
	}
	if err := m.Disconnect(ctx, "alice", "owner", "gitlab:code.corp", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.revoked, ",") != "at-2" {
		t.Fatalf("revoked = %v", f.revoked)
	}
}
