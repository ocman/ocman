package linkpreview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/previewauth"
	"github.com/NoUseFreak/ocman/internal/state"
)

const lifecycleCallback = "https://ocman.test/api/previews/oauth/callback"

// lifecycleOAuth is one provider's mock authorization server in front of
// that provider's mock API. Tokens it issues are aliased to the API mock's
// known account (alias) while live, and to a rejected token once revoked.
type lifecycleOAuth struct {
	api   http.HandlerFunc
	alias string

	mu        sync.Mutex
	access    map[string]bool
	refresh   map[string]bool
	n         int
	refreshes int
	revoked   []string
	expiresIn int  // 0: token never expires
	noRefresh bool // issue no refresh token
}

func (l *lifecycleOAuth) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/lc/token":
		l.token(w, r)
		return
	case "/lc/revoke":
		l.mu.Lock()
		l.revoked = append(l.revoked, l.params(r)["token"])
		l.mu.Unlock()
		return
	}
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		l.mu.Lock()
		live := l.access[tok]
		l.mu.Unlock()
		alias := "tok-bad"
		if live {
			alias = l.alias
		}
		r.Header.Set("Authorization", "Bearer "+alias)
	}
	l.api(w, r)
}

// params reads a form or flat JSON body (Notion, Jira).
func (l *lifecycleOAuth) params(r *http.Request) map[string]string {
	out := map[string]string{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&out)
	} else {
		_ = r.ParseForm()
		for k := range r.PostForm {
			out[k] = r.PostForm.Get(k)
		}
	}
	if id, _, ok := r.BasicAuth(); ok {
		out["client_id"] = id
	}
	return out
}

func (l *lifecycleOAuth) token(w http.ResponseWriter, r *http.Request) {
	p := l.params(r)
	l.mu.Lock()
	defer l.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fail := func() {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}
	if p["client_id"] != "cid" {
		fail()
		return
	}
	switch p["grant_type"] {
	case "authorization_code":
		if p["code"] != "code1" || p["redirect_uri"] != lifecycleCallback {
			fail()
			return
		}
	case "refresh_token":
		if !l.refresh[p["refresh_token"]] {
			fail()
			return
		}
		delete(l.refresh, p["refresh_token"]) // rotation
		l.refreshes++
	default:
		fail()
		return
	}
	l.n++
	access := fmt.Sprintf("lc-at-%d", l.n)
	l.access[access] = true
	// ok/workspace/owner satisfy Slack's and Notion's token decoders.
	body := map[string]any{
		"ok": true, "access_token": access, "token_type": "bearer",
		"workspace_id": "W1", "workspace_name": "Acme", "owner": map[string]any{"user": map[string]any{"name": "alice"}},
	}
	if l.expiresIn > 0 {
		body["expires_in"] = l.expiresIn
	}
	if !l.noRefresh {
		rt := fmt.Sprintf("lc-rt-%d", l.n)
		l.refresh[rt] = true
		body["refresh_token"] = rt
	}
	_ = json.NewEncoder(w).Encode(body)
}

// revokeAll invalidates every access and refresh token at the provider.
func (l *lifecycleOAuth) revokeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.access, l.refresh = map[string]bool{}, map[string]bool{}
}

type lifecycleCase struct {
	name string
	// setup returns the API mock, its live account alias, and the
	// provider app and resolver pointed at srv.
	setup func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver)
	// private needs the viewer's grant; public (forges only) must preview
	// without connecting, as before viewer consent existed.
	private, public string
	rules           []IdentifierRule
}

func lifecycleCases() []lifecycleCase {
	return []lifecycleCase{{
		name: "github",
		setup: func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver) {
			_, api, _ := newForgeHarness(t, false, true)
			return api.serve, "tok-alice", GitHubOAuth("cid", "sec", srv.URL),
				Forge{ID: "github", Host: "github.com", APIBase: srv.URL, Connectable: true}
		},
		private: "https://github.com/acme/priv/pull/2",
		public:  "https://github.com/acme/pub/pull/1",
	}, {
		name: "forgejo",
		setup: func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver) {
			_, api, _ := newForgeHarness(t, true, true)
			host := srv.Listener.Addr().String()
			return api.serve, "tok-alice", ForgejoOAuth(host, "cid", "sec"),
				Forge{ID: "forgejo:" + host, Host: host, APIBase: srv.URL + "/api/v1", Gitea: true, Connectable: true}
		},
		private: "https://{host}/acme/priv/pulls/2",
		public:  "https://{host}/acme/pub/pulls/1",
	}, {
		name: "gitlab",
		setup: func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver) {
			allowLoopback(t)
			api, _ := newGitLabServer(t, "tok-alice")
			return api.serve, "tok-alice", GitLabOAuth("code.corp", "cid", "", srv.URL+"/api/v4", srv.URL),
				GitLab{Host: "code.corp", APIBase: srv.URL + "/api/v4"}
		},
		private: "https://code.corp/grp/secret/-/issues/3",
		public:  "https://code.corp/grp/sub/app/-/merge_requests/7",
	}, {
		name: "slack",
		setup: func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver) {
			_, api, _, _, _ := newSlackHarness(t)
			return api.serve, "tok-alice", SlackOAuth("cid", "sec", srv.URL+"/api"), Slack{APIBase: srv.URL + "/api"}
		},
		private: "https://acme.slack.com/archives/C1/p1700000000000100",
	}, {
		name: "notion",
		setup: func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver) {
			_, api, _, _ := newNotionHarness(t)
			return api.serve, "tok-alice", NotionOAuth("cid", "sec", srv.URL+"/v1"), Notion{APIBase: srv.URL + "/v1"}
		},
		private: "https://www.notion.so/acme/Launch-plan-" + pageA,
	}, {
		name: "linear",
		setup: func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver) {
			api, _ := newLinearHarness(t)
			return api.serve, "tok-alice", LinearOAuth("cid", "", srv.URL), Linear{APIBase: srv.URL}
		},
		private: "https://linear.app/acme/issue/ENG-1/fix-login",
	}, {
		name: "jira",
		setup: func(t *testing.T, srv *httptest.Server) (http.HandlerFunc, string, previewauth.Provider, Resolver) {
			api, _ := newJiraHarness(t)
			return api.serve, "tok-one", JiraOAuth("cid", "sec", srv.URL, srv.URL), Jira{APIBase: srv.URL}
		},
		private: "see ABC-1",
		rules:   jiraRule,
	}}
}

// TestProviderLifecycle drives every provider through connect, callback,
// preview, refresh, restart, expiry, provider-side revocation and
// disconnect against its mock API, with a disposable state DB.
func TestProviderLifecycle(t *testing.T) {
	for _, tc := range lifecycleCases() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			l := &lifecycleOAuth{access: map[string]bool{}, refresh: map[string]bool{}, expiresIn: 30}
			srv := httptest.NewTLSServer(http.HandlerFunc(l.serve))
			t.Cleanup(srv.Close)
			var p previewauth.Provider
			var res Resolver
			l.api, l.alias, p, res = tc.setup(t, srv)
			p.TokenURL = srv.URL + "/lc/token"
			if p.RevokeURL != "" {
				p.RevokeURL = srv.URL + "/lc/revoke"
			}
			host := strings.NewReplacer("{host}", srv.Listener.Addr().String())
			private, public := host.Replace(tc.private), host.Replace(tc.public)

			path := filepath.Join(t.TempDir(), "state.db")
			var db *state.DB
			var m *previewauth.Manager
			var svc *Service
			open := func() {
				var err error
				if db, err = state.Open(path); err != nil {
					t.Fatal(err)
				}
				m = previewauth.New(db, lifecycleCallback, srv.Client(), p)
				svc = New(m, srv.Client(), res)
			}
			open()
			t.Cleanup(func() { db.Close() })
			resolve := func(text string) Preview {
				t.Helper()
				refs := svc.Discover(text, tc.rules)
				if len(refs) != 1 {
					t.Fatalf("discover %q = %v", text, refs)
				}
				return svc.Resolve(ctx, "alice", "owner", refs)[0]
			}
			expect := func(step string, want State) {
				t.Helper()
				got := resolve(private)
				if got.State != want || (want == StateOK) != (got.Title != "") {
					t.Fatalf("%s: preview = %+v, want %s", step, got, want)
				}
			}
			connected := func() int {
				ws, err := m.Workspaces(ctx, "alice", "owner", p.ID)
				if err != nil {
					t.Fatal(err)
				}
				return len(ws)
			}
			connect := func() {
				t.Helper()
				authURL, err := m.Begin(ctx, "alice", "owner", p.ID, "/")
				if err != nil {
					t.Fatal(err)
				}
				u, _ := url.Parse(authURL)
				q := u.Query()
				if q.Get("redirect_uri") != lifecycleCallback || q.Get("client_id") != "cid" || q.Has("client_secret") ||
					p.PKCE != (q.Get("code_challenge_method") == "S256") {
					t.Fatalf("authorize URL = %s", authURL)
				}
				if _, err := m.Complete(ctx, "alice", q.Get("state"), "code1", ""); err != nil {
					t.Fatalf("callback: %v", err)
				}
				if connected() != 1 {
					t.Fatal("callback stored no grant")
				}
			}

			// Before consent: private resources offer Connect, public forge
			// links still preview with no login.
			expect("before connect", StateConnect)
			if public != "" {
				if got := resolve(public); got.State != StateOK {
					t.Fatalf("public link without login = %+v", got)
				}
			}

			// Connect + callback, then preview. expires_in 30 is inside the
			// refresh skew, so the first use refreshes and rotates.
			connect()
			expect("connected", StateOK)
			if l.refreshes == 0 {
				t.Fatal("expiring token was not refreshed")
			}

			// Reload: a restarted process keeps the grant.
			db.Close()
			open()
			expect("after reload", StateOK)

			// Refresh token revoked at the provider: the grant is forgotten.
			l.revokeAll()
			expect("refresh revoked", StateConnect)
			if connected() != 0 {
				t.Fatal("revoked grant kept")
			}

			// Expired with no refresh token.
			l.noRefresh = true
			connect()
			expect("expired", StateExpired)

			// Access token revoked at the provider (401) once cached data
			// ages out: the grant is forgotten.
			l.noRefresh, l.expiresIn = false, 0
			connect()
			expect("reconnected", StateOK)
			l.revokeAll()
			svc.Purge("", "", "")
			expect("access revoked", StateConnect)
			if connected() != 0 {
				t.Fatal("401 did not forget the grant")
			}

			// Disconnect revokes at the provider where it has an endpoint,
			// and cached data is never served afterwards.
			connect()
			expect("before disconnect", StateOK)
			if err := m.Disconnect(ctx, "alice", "owner", p.ID, ""); err != nil {
				t.Fatal(err)
			}
			if (p.RevokeURL != "") != (len(l.revoked) == 1) {
				t.Fatalf("revoked = %v (revoke URL %q)", l.revoked, p.RevokeURL)
			}
			expect("disconnected", StateConnect)
		})
	}
}
