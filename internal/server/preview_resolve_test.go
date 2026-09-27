package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
)

type trackerResolver struct{ api string }

var trackerID = regexp.MustCompile(`^[A-Z]+-[0-9]+$`)

func (r trackerResolver) Provider() string { return "mock" }
func (r trackerResolver) APIHosts() []string {
	u, _ := url.Parse(r.api)
	return []string{u.Host}
}
func (r trackerResolver) ParseURL(u *url.URL) (linkpreview.Ref, bool) {
	id, ok := strings.CutPrefix(u.Path, "/browse/")
	if u.Host != "tracker.example.com" || !ok {
		return linkpreview.Ref{}, false
	}
	return r.ParseIdentifier(id)
}
func (r trackerResolver) ParseIdentifier(id string) (linkpreview.Ref, bool) {
	return linkpreview.Ref{Kind: "issue", ID: id}, trackerID.MatchString(id)
}
func (r trackerResolver) Fetch(ctx context.Context, api *linkpreview.API, ref linkpreview.Ref) (linkpreview.Preview, error) {
	var body struct{ Title string }
	err := api.JSON(ctx, http.MethodGet, r.api, []string{"issues", ref.ID}, nil, nil, &body)
	return linkpreview.Preview{Title: body.Title}, err
}

func resolvePreviews(t *testing.T, b *browser, s *Server, text string) []linkpreview.Preview {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text})
	rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", string(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve = %d %s", rr.Code, rr.Body.String())
	}
	var resp struct{ Previews []linkpreview.Preview }
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Previews
}

func TestPreviewResolve_ViewerScopedWithRuleFallback(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"title":"Private %s"}`, strings.TrimPrefix(r.URL.Path, "/issues/"))
	}))
	defer api.Close()
	m := newMockOAuth(t)
	s := previewServer(t, m, "").WithPreviewResolvers(trackerResolver{api: api.URL})
	s.previewAuth.client = api.Client()
	alice, bob := newBrowser(), newBrowser()

	rules := `{"rules":[{"pattern":"\\b([A-Z]+-\\d+)\\b","replacement":"https://tracker.example.com/browse/$1","provider":"mock"}]}`
	if rr := alice.do(t, s, http.MethodPost, "/api/settings/link-preview-rules", rules); rr.Code != http.StatusOK {
		t.Fatalf("rules = %d %s", rr.Code, rr.Body.String())
	}
	text := "Fix https://tracker.example.com/browse/ABC-1 (ABC-1) then ABC-2."

	// Not connected: resources are discovered but nothing private resolves.
	got := resolvePreviews(t, alice, s, text)
	if len(got) != 2 || got[0].State != linkpreview.StateConnect || got[0].Title != "" {
		t.Fatalf("before connect = %+v", got)
	}

	alice.do(t, s, http.MethodGet, alice.connect(t, s, m, "code-a", "/"), "")
	if got = resolvePreviews(t, alice, s, text); got[0].State != linkpreview.StateAmbiguous || got[0].Title != "" {
		t.Fatalf("two workspaces = %+v", got)
	}
	body, _ := json.Marshal(map[string]any{"text": text, "workspaces": map[string]string{"mock": "w2"}})
	var chosen struct{ Previews []linkpreview.Preview }
	_ = json.Unmarshal(alice.do(t, s, http.MethodPost, "/api/previews/resolve", string(body)).Body.Bytes(), &chosen)
	if len(chosen.Previews) != 2 || chosen.Previews[0].Workspace != "w2" || chosen.Previews[0].Title != "Private ABC-1" {
		t.Fatalf("chosen workspace = %+v", chosen.Previews)
	}
	alice.do(t, s, http.MethodPost, "/api/previews/disconnect", `{"provider":"mock","workspaceId":"w2"}`)
	got = resolvePreviews(t, alice, s, text)
	if len(got) != 2 || got[0].Title != "Private ABC-1" || got[1].Title != "Private ABC-2" || got[0].Workspace != "w1" {
		t.Fatalf("connected = %+v", got)
	}
	for _, p := range resolvePreviews(t, bob, s, text) {
		if p.State != linkpreview.StateConnect || p.Title != "" {
			t.Fatalf("bob sees %+v", p)
		}
	}

	alice.do(t, s, http.MethodPost, "/api/previews/disconnect", `{"provider":"mock"}`)
	for _, p := range resolvePreviews(t, alice, s, text) {
		if p.State != linkpreview.StateConnect || p.Title != "" {
			t.Fatalf("stale after disconnect: %+v", p)
		}
	}
}

func TestPreviewResolve_RejectsOversizedAndRemote(t *testing.T) {
	s := previewServer(t, newMockOAuth(t), "")
	b := newBrowser()
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"`+strings.Repeat("a", maxPreviewResolveBody)+`"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized = %d", rr.Code)
	}
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve?remoteId=elsewhere", `{"text":"x"}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("remote = %d", rr.Code)
	}
	if got := resolvePreviews(t, b, s, strings.Repeat("https://example.com/x ", 20000)); len(got) != 0 {
		t.Fatalf("unknown links resolved: %+v", got)
	}
}

func TestLinkPreviewRules_RejectsInvalidProvider(t *testing.T) {
	err := validateLinkPreviewRules(linkPreviewRules{Rules: []linkPreviewRule{{Pattern: `A-\d+`, Replacement: "https://x.example/$&", Provider: "Bad/../x"}}})
	if err == nil {
		t.Fatal("invalid provider accepted")
	}
}

func resolveAt(t *testing.T, b *browser, s *Server, remote, text string) []linkpreview.Preview {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text})
	rr := b.do(t, s, http.MethodPost, "/api/previews/resolve?remoteId="+remote, string(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve %s = %d %s", remote, rr.Code, rr.Body.String())
	}
	var resp struct{ Previews []linkpreview.Preview }
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Previews
}

func connectAt(t *testing.T, b *browser, s *Server, m *mockOAuth, remote, code string) {
	t.Helper()
	rr := b.do(t, s, http.MethodPost, "/api/previews/connect?remoteId="+remote, `{"provider":"mock"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("connect %s = %d %s", remote, rr.Code, rr.Body.String())
	}
	var resp struct{ AuthorizeURL string }
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	// The provider redirects to the shared callback without remoteId.
	if rr := b.do(t, s, http.MethodGet, m.authorize(t, resp.AuthorizeURL, code), ""); rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "previewAuth=connected") {
		t.Fatalf("callback %s = %d %s", remote, rr.Code, rr.Header().Get("Location"))
	}
	b.do(t, s, http.MethodPost, "/api/previews/disconnect?remoteId="+remote, `{"provider":"mock","workspaceId":"w2"}`)
}

// Two hosts with distinct grants for the same provider and two browsers:
// each (browser, host) pair sees only previews fetched with its own grant,
// and a disconnected host fails closed and loses its cached previews.
func TestPreviewResolve_OwnerQualifiedAcrossRemotes(t *testing.T) {
	var fetches atomic.Int32
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		fmt.Fprintf(w, `{"title":"Private via %s"}`, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}))
	defer api.Close()
	m := newMockOAuth(t)
	s := previewServer(t, m, "").WithPreviewResolvers(trackerResolver{api: api.URL})
	s.previewAuth.client = api.Client()
	s.router().RegisterRemote("r1", s.router().Local())
	alice, bob := newBrowser(), newBrowser()
	const text = "https://tracker.example.com/browse/ABC-1"

	connectAt(t, alice, s, m, "local", "code-a") // access-1
	connectAt(t, alice, s, m, "r1", "code-b")    // access-2
	connectAt(t, bob, s, m, "r1", "code-c")      // access-3

	for _, c := range []struct {
		b      *browser
		remote string
		want   string
	}{
		{alice, "local", "Private via rotated-refresh-1"},
		{alice, "r1", "Private via rotated-refresh-2"},
		{bob, "r1", "Private via rotated-refresh-3"},
		{bob, "local", ""},
	} {
		got := resolveAt(t, c.b, s, c.remote, text)
		if len(got) != 1 || got[0].Title != c.want {
			t.Fatalf("%s: got %+v, want %q", c.remote, got, c.want)
		}
	}

	s.router().UnregisterRemote("r1")
	rr := alice.do(t, s, http.MethodPost, "/api/previews/resolve?remoteId=r1", `{"text":"`+text+`"}`)
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "Private") {
		t.Fatalf("disconnected owner = %d %s", rr.Code, rr.Body.String())
	}
	// Hub grants never stand in for the disconnected remote.
	if got := resolveAt(t, alice, s, "local", text); got[0].Title != "Private via rotated-refresh-1" {
		t.Fatalf("hub after disconnect = %+v", got)
	}

	before := fetches.Load()
	s.router().RegisterRemote("r1", s.router().Local())
	if got := resolveAt(t, alice, s, "r1", text); got[0].Title != "Private via rotated-refresh-2" || fetches.Load() != before+1 {
		t.Fatalf("reconnect served purged cache: %+v fetches %d->%d", got, before, fetches.Load())
	}
}

// Forge links: public ones render without login; a private one uses the
// owner machine's token only for the owner's own direct request, never
// for a proxied viewer or a remote owner's content.
func TestPreviewResolve_ForgeOwnerFallback(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner := r.Header.Get("Authorization") == "Bearer tok-owner"
		switch {
		case r.URL.Path == "/repos/acme/pub":
			fmt.Fprint(w, `{"private":false}`)
		case r.URL.Path == "/repos/acme/pub/pulls/1":
			fmt.Fprint(w, `{"title":"Public PR","state":"open"}`)
		case r.URL.Path == "/repos/acme/priv" && owner:
			fmt.Fprint(w, `{"private":true}`)
		case r.URL.Path == "/repos/acme/priv/pulls/2" && owner:
			fmt.Fprint(w, `{"title":"Private PR","state":"open"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	s := previewServer(t, newMockOAuth(t), "").WithPreviewResolvers(
		linkpreview.Forge{ID: "github", Host: "github.com", APIBase: api.URL, OwnerToken: "tok-owner"})
	s.previewAuth.client = api.Client()
	s.router().RegisterRemote("r1", s.router().Local())
	const text = `{"text":"https://github.com/acme/pub/pull/1 https://github.com/acme/priv/pull/2"}`
	titles := func(rr *httptest.ResponseRecorder) string {
		t.Helper()
		var resp struct{ Previews []linkpreview.Preview }
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &resp) != nil || len(resp.Previews) != 2 {
			t.Fatalf("resolve = %d %s", rr.Code, rr.Body.String())
		}
		return resp.Previews[0].Title + "|" + resp.Previews[1].Title
	}
	b := newBrowser()
	if got := titles(b.do(t, s, http.MethodPost, "/api/previews/resolve", text)); got != "Public PR|Private PR" {
		t.Fatalf("owner = %q", got)
	}
	if got := titles(b.do(t, s, http.MethodPost, "/api/previews/resolve?remoteId=r1", text)); got != "Public PR|" {
		t.Fatalf("remote owner = %q", got)
	}
	mux, _ := s.routes()
	req := httptest.NewRequest(http.MethodPost, "/api/previews/resolve", strings.NewReader(text))
	req.RemoteAddr, req.Host = "127.0.0.1:1", "localhost:8228"
	req.Header.Set("X-Forwarded-For", "192.0.2.4")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if got := titles(rr); got != "Public PR|" {
		t.Fatalf("proxied viewer = %q", got)
	}
}

func TestPreviewForges_FromEnv(t *testing.T) {
	t.Setenv("OCMAN_GITHUB_PREVIEW_CLIENT_ID", "gid")
	t.Setenv("OCMAN_GITHUB_PREVIEW_CLIENT_SECRET", "gsecret")
	t.Setenv("OCMAN_FORGEJO_PREVIEW_APPS", "Code.Example.com=fid:fsec:ret, bad host=x:y,nocreds.example.com=,")
	s := testServer(t)
	registerForgejoClient(s, "tea.example.com", "https://tea.example.com")
	m := s.previewManager()
	for _, id := range []string{"github", "forgejo:code.example.com"} {
		if _, ok := m.Provider(id); !ok {
			t.Fatalf("provider %s missing", id)
		}
	}
	if p, _ := m.Provider("forgejo:code.example.com"); p.ClientSecret != "fsec:ret" {
		t.Fatalf("secret = %q", p.ClientSecret)
	}
	if _, ok := m.Provider("forgejo:nocreds.example.com"); ok {
		t.Fatal("entry without credentials registered")
	}
	refs := s.linkPreviews().Discover("https://github.com/a/b/pull/1 https://code.example.com/a/b/pulls/2 "+
		"https://tea.example.com/a/b/issues/3 https://nocreds.example.com/a/b/pulls/4", nil)
	var got []string
	for _, r := range refs {
		got = append(got, r.Provider)
	}
	if strings.Join(got, ",") != "github,forgejo:code.example.com,forgejo:tea.example.com" {
		t.Fatalf("providers = %v", got)
	}
}

func TestPreviewGitLab_FromEnv(t *testing.T) {
	t.Setenv("OCMAN_GITLAB_PREVIEW_APPS", "GitLab.com=gid:gsec, code.corp:8443=cid,bad host=x,noid.example=,code.corp:8443=dup")
	s := testServer(t)
	m := s.previewManager()
	if p, ok := m.Provider("gitlab:gitlab.com"); !ok || p.ClientSecret != "gsec" || !p.PKCE || p.Scopes[0] != "read_api" {
		t.Fatalf("gitlab.com provider = %+v %v", p, ok)
	}
	if p, ok := m.Provider("gitlab:code.corp:8443"); !ok || p.ClientID != "cid" || p.TokenURL != "https://code.corp:8443/oauth/token" {
		t.Fatalf("self-managed provider = %+v %v", p, ok)
	}
	refs := s.linkPreviews().Discover("https://gitlab.com/a/b/-/merge_requests/1 https://code.corp:8443/g/s/p/-/issues/2 "+
		"https://code.corp/g/p/-/issues/3 https://gitlab.example.com/g/p/-/issues/4 https://noid.example/g/p/-/issues/5", nil)
	var got []string
	for _, r := range refs {
		got = append(got, r.Provider)
	}
	if strings.Join(got, ",") != "gitlab:gitlab.com,gitlab:code.corp:8443" {
		t.Fatalf("providers = %v", got)
	}
}
