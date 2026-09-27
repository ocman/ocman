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

// jiraAPI fakes api.atlassian.com and auth.atlassian.com.
type jiraAPI struct {
	mu     sync.Mutex
	sites  map[string][]string          // token -> cloud IDs
	issues map[string]map[string]string // cloud ID -> key -> summary
	status map[string]int               // "cloud/key" -> forced status
	hosts  map[string]string            // cloud ID -> site host
	calls  []string                     // API paths requested

	refresh   map[string]bool
	refreshN  int
	tokenBody map[string]any
}

func (f *jiraAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/oauth/token" {
		f.token(w, r)
		return
	}
	f.calls = append(f.calls, r.URL.Path)
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	clouds, ok := f.sites[tok]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/me":
		_ = json.NewEncoder(w).Encode(map[string]any{"account_id": "acc-" + tok, "name": "Alice"})
		return
	case "/oauth/token/accessible-resources":
		res := []any{
			// Not a Jira resource, and a site with a hostile URL: both ignored.
			map[string]any{"id": "conf", "name": "Wiki", "url": "https://wiki.atlassian.net", "scopes": []string{"read:confluence-content.all"}},
			map[string]any{"id": "evil", "name": "Evil", "url": "https://evil.example.com", "scopes": []string{"read:jira-work"}},
		}
		for _, c := range clouds {
			res = append(res, map[string]any{"id": c, "name": strings.ToUpper(c), "url": "https://" + f.hosts[c], "scopes": []string{"read:jira-work", "read:me"}})
		}
		_ = json.NewEncoder(w).Encode(res)
		return
	}
	var cloud, key string
	if _, err := fmt.Sscanf(strings.ReplaceAll(r.URL.Path, "/", " "), " ex jira %s rest api 3 issue %s", &cloud, &key); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !contains(clouds, cloud) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if st := f.status[cloud+"/"+key]; st != 0 {
		w.WriteHeader(st)
		return
	}
	summary, found := f.issues[cloud][key]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`))
		return
	}
	fields := map[string]any{
		"summary": summary, "updated": "2026-09-01T10:00:00.000+0200",
		"status": map[string]any{"name": "In Progress"}, "issuetype": map[string]any{"name": "Bug"}, "assignee": nil,
	}
	if key == "ABC-1" {
		fields["assignee"] = map[string]any{"displayName": "Bob"}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"key": key, "self": "https://evil.example.com/x", "fields": fields})
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// token implements authorization_code and rotating refresh_token grants
// with Atlassian's JSON bodies; a dead refresh token is 403 invalid_grant.
func (f *jiraAPI) token(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.tokenBody = body
	issue := func() {
		f.refreshN++
		access, rt := fmt.Sprintf("at-%d", f.refreshN), fmt.Sprintf("rt-%d", f.refreshN)
		f.sites[access], f.refresh[rt] = []string{"c1", "c2"}, true
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": rt, "expires_in": 30})
	}
	switch body["grant_type"] {
	case "authorization_code":
		if body["code"] != "code1" || body["client_secret"] != "sec" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		issue()
	case "refresh_token":
		rt, _ := body["refresh_token"].(string)
		if !f.refresh[rt] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Unknown or invalid refresh token."}`))
			return
		}
		delete(f.refresh, rt)
		issue()
	}
}

func newJiraHarness(t *testing.T) (*jiraAPI, *httptest.Server) {
	api := &jiraAPI{
		sites: map[string][]string{"tok-one": {"c1"}, "tok-multi": {"c1", "c2"}},
		hosts: map[string]string{"c1": "acme.atlassian.net", "c2": "beta.atlassian.net", "c3": "gamma.atlassian.net"},
		issues: map[string]map[string]string{
			"c1": {"ABC-1": "Fix login", "ABC-2": "Shared key", "ABC-3": "Locked"},
			"c2": {"ABC-2": "Beta shared key"},
			"c3": {"ABC-1": "Gamma secret"},
		},
		status:  map[string]int{"c1/ABC-3": http.StatusForbidden},
		refresh: map[string]bool{},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	return api, srv
}

var jiraRule = []IdentifierRule{{Pattern: `\bABC-\d+\b`, Provider: "jira"}}

func jiraResolve(svc *Service, viewer, text string) []Preview {
	return svc.Resolve(context.Background(), viewer, "owner", svc.Discover(text, jiraRule))
}

func TestJiraParseURL(t *testing.T) {
	text := "https://acme.atlassian.net/browse/ABC-12 https://Beta.Atlassian.NET/browse/abc-13 " +
		"http://acme.atlassian.net/browse/ABC-14 https://acme.atlassian.net.evil.com/browse/ABC-15 " +
		"https://a.b.atlassian.net/browse/ABC-16 https://acme.atlassian.net/jira/ABC-17 https://acme.atlassian.net:444/browse/ABC-18 " +
		"https://acme.atlassian.net/browse/ABC-0 https://atlassian.net/browse/ABC-19"
	var got []string
	for _, r := range Discover(text, []Resolver{Jira{}}, nil) {
		got = append(got, r.Kind+" "+r.ID+" "+r.URL)
	}
	want := "issue ABC-12 https://acme.atlassian.net/browse/ABC-12\nissue ABC-13 https://beta.atlassian.net/browse/ABC-13"
	if strings.Join(got, "\n") != want {
		t.Fatalf("refs:\n%s", strings.Join(got, "\n"))
	}
	if _, ok := (Jira{}).ParseIdentifier("../1"); ok {
		t.Fatal("unsafe identifier accepted")
	}
}

func TestJiraIssueCards(t *testing.T) {
	api, srv := newJiraHarness(t)
	tokens := &fakeTokens{grants: map[string]string{
		"alice/acc": "tok-one", "multi/acc": "tok-multi", "eve/acc": "tok-revoked",
	}}
	svc := New(tokens, srv.Client(), Jira{APIBase: srv.URL})

	ps := jiraResolve(svc, "alice", "https://acme.atlassian.net/browse/ABC-1 and ABC-2")
	if p := ps[0]; p.State != StateOK || p.Title != "Fix login" || p.Status != "In Progress" ||
		strings.Join(p.Meta, ",") != "Bug,Bob,C1" || p.URL != "https://acme.atlassian.net/browse/ABC-1" || p.UpdatedAt.IsZero() {
		t.Fatalf("link = %+v", p)
	}
	if p := ps[1]; p.State != StateOK || p.Title != "Shared key" || strings.Join(p.Meta, ",") != "Bug,Unassigned,C1" {
		t.Fatalf("identifier = %+v", p)
	}
	// The API host is always api.atlassian.com/ex/jira/{cloudid}.
	for _, c := range api.calls {
		if !strings.HasPrefix(c, "/oauth/token/accessible-resources") && !strings.HasPrefix(c, "/ex/jira/c1/rest/api/3/issue/") {
			t.Fatalf("unexpected call %s", c)
		}
	}
	// 404 and 403.
	if p := jiraResolve(svc, "alice", "ABC-404")[0]; p.State != StateNotFound || p.Title != "" {
		t.Fatalf("missing = %+v", p)
	}
	if p := jiraResolve(svc, "alice", "https://acme.atlassian.net/browse/ABC-3")[0]; p.State != StateDenied || p.Title != "" {
		t.Fatalf("forbidden = %+v", p)
	}
	// A site the token cannot reach is not_found, even though gamma has ABC-1.
	if p := jiraResolve(svc, "alice", "https://gamma.atlassian.net/browse/ABC-1")[0]; p.State != StateNotFound || p.Title != "" {
		t.Fatalf("unreachable site = %+v", p)
	}
	// Disconnected.
	if p := jiraResolve(svc, "mallory", "https://acme.atlassian.net/browse/ABC-1")[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("mallory = %+v", p)
	}

	// Multisite: a link picks its own site; an identifier found on both
	// sites is a chooser, found on one resolves directly.
	if p := jiraResolve(svc, "multi", "https://beta.atlassian.net/browse/ABC-2")[0]; p.State != StateOK || p.Title != "Beta shared key" {
		t.Fatalf("beta link = %+v", p)
	}
	p := jiraResolve(svc, "multi", "ABC-2")[0]
	if p.State != StateAmbiguous || p.Title != "" || len(p.Choices) != 2 ||
		p.Choices[0].URL != "https://acme.atlassian.net/browse/ABC-2" || p.Choices[1].Title != "C2: Beta shared key" {
		t.Fatalf("ambiguous = %+v", p)
	}
	if p := jiraResolve(svc, "multi", "ABC-1")[0]; p.State != StateOK || p.URL != "https://acme.atlassian.net/browse/ABC-1" {
		t.Fatalf("single hit = %+v", p)
	}

	// A revoked access token (401) forgets the grant.
	if p := jiraResolve(svc, "eve", "ABC-1")[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("revoked = %+v", p)
	}
	if strings.Join(tokens.revoked, ",") != "eve/acc" {
		t.Fatalf("revoked = %v", tokens.revoked)
	}
}

func TestJiraConnectRotateAndRevoke(t *testing.T) {
	api, srv := newJiraHarness(t)
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	m := previewauth.New(db, "https://ocman.test/cb", srv.Client(), JiraOAuth("cid", "sec", srv.URL, srv.URL))
	svc := New(m, srv.Client(), Jira{APIBase: srv.URL})

	authURL, err := m.Begin(ctx, "alice", "owner", "jira", "/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if u.Path != "/authorize" || q.Get("scope") != "read:jira-work read:me offline_access" || q.Get("audience") != "api.atlassian.com" || q.Get("prompt") != "consent" {
		t.Fatalf("authorize = %s", authURL)
	}
	if _, err := m.Complete(ctx, "alice", q.Get("state"), "code1", ""); err != nil {
		t.Fatal(err)
	}
	if api.tokenBody["redirect_uri"] != "https://ocman.test/cb" {
		t.Fatalf("token body = %v", api.tokenBody)
	}
	st, _ := m.Status(ctx, "alice", "owner")
	if len(st) != 1 || len(st[0].Connections) != 1 {
		t.Fatalf("status = %+v", st)
	}
	c0 := st[0].Connections[0]
	if c0.WorkspaceID != "acc-at-1" || c0.AccountName != "Alice" || len(c0.Sites) != 2 || c0.Sites[1].ID != "c2" {
		t.Fatalf("connection = %+v", c0)
	}

	// Inside the refresh skew: resolving refreshes and rotates.
	if p := jiraResolve(svc, "alice", "https://acme.atlassian.net/browse/ABC-1")[0]; p.State != StateOK || p.Title != "Fix login" {
		t.Fatalf("refreshed = %+v", p)
	}
	c, _ := db.PreviewCredential(ctx, "alice", "owner", "jira", "acc-at-1")
	if c.RefreshToken != "rt-2" || c.AccessToken != "at-2" {
		t.Fatalf("not rotated: %s %s", c.AccessToken, c.RefreshToken)
	}
	if p := jiraResolve(svc, "alice", "https://beta.atlassian.net/browse/ABC-2")[0]; p.State != StateOK {
		t.Fatalf("second rotation = %+v", p)
	}

	// A refresh token Atlassian revoked (403 invalid_grant) forgets the grant.
	api.mu.Lock()
	api.refresh = map[string]bool{}
	api.mu.Unlock()
	if p := jiraResolve(svc, "alice", "ABC-1")[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("revoked refresh = %+v", p)
	}
	if ws, _ := m.Workspaces(ctx, "alice", "owner", "jira"); len(ws) != 0 {
		t.Fatalf("grant kept: %v", ws)
	}
}
