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

const (
	pageA = "0123456789abcdef0123456789abcdef"
	pageB = "fedcba9876543210fedcba9876543210"
)

func dashed(h string) string { return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:] }

func notionPageJSON(id, title string, extra map[string]any) map[string]any {
	p := map[string]any{
		"object": "page", "id": dashed(id), "url": "https://www.notion.so/" + strings.ReplaceAll(title, " ", "-") + "-" + id,
		"last_edited_time": "2026-09-01T10:00:00.000Z", "icon": map[string]any{"type": "emoji", "emoji": "📘"},
		"properties": map[string]any{"Name": map[string]any{"type": "title", "title": []any{map[string]any{"plain_text": title}}}},
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// notionAPI fakes the Notion API: each token sees only its shared pages.
type notionAPI struct {
	mu     sync.Mutex
	shared map[string][]map[string]any // token -> pages
	calls  []string
}

func (f *notionAPI) serve(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, tok+" "+r.Method+" "+r.URL.Path)
	if r.Header.Get("Notion-Version") != notionVersion {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	pages, ok := f.shared[tok]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/pages/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/pages/")
		for _, p := range pages {
			if p["id"] == id {
				_ = json.NewEncoder(w).Encode(p)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/search":
		var body struct {
			Query  string            `json:"query"`
			Filter map[string]string `json:"filter"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Filter["value"] != "page" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var hits []any
		for _, p := range pages {
			if b, _ := json.Marshal(p["properties"]); strings.Contains(strings.ToLower(string(b)), strings.ToLower(body.Query)) {
				hits = append(hits, p)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "results": hits})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newNotionHarness(t *testing.T) (*Service, *notionAPI, *fakeTokens, *httptest.Server) {
	api := &notionAPI{shared: map[string][]map[string]any{
		"tok-alice": {
			notionPageJSON(pageA, "ABC-42 Launch plan", nil),
			notionPageJSON(pageB, "ABC-7 Retro", nil),
			notionPageJSON("11111111111111111111111111111111", "ABC-7 follow-up", nil),
			notionPageJSON("22222222222222222222222222222222", "ABC-420 Other", nil),
			notionPageJSON("33333333333333333333333333333333", "ABC-9 Trashed", map[string]any{"in_trash": true}),
		},
		"tok-bob": {notionPageJSON(pageB, "ABC-7 Retro", nil)},
	}}
	srv := httptest.NewTLSServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	tokens := &fakeTokens{grants: map[string]string{"alice/W1": "tok-alice", "bob/W2": "tok-bob", "eve/W1": "tok-gone"}}
	return New(tokens, srv.Client(), Notion{APIBase: srv.URL + "/v1"}), api, tokens, srv
}

var ticketRule = []IdentifierRule{{Pattern: `\bABC-\d+\b`, Provider: "notion"}}

func notionResolve(svc *Service, viewer, text string) []Preview {
	return svc.Resolve(context.Background(), viewer, "owner", svc.Discover(text, ticketRule))
}

func TestNotionParseURL(t *testing.T) {
	text := "https://www.notion.so/acme/Launch-plan-" + pageA + " " +
		"https://notion.so/" + pageA + "#block " + // dup
		"https://app.notion.com/p/" + dashed(pageB) + " " +
		"https://acme.notion.site/Public-" + strings.ToUpper("11111111111111111111111111111111") + " " +
		"https://www.notion.so/acme/Board-" + pageB + "?v=abc&p=22222222222222222222222222222222&pm=s " +
		"http://www.notion.so/" + pageA + " https://notion.so.evil.com/" + pageA + " https://www.notion.so/acme/Short-0123 " +
		"https://www.notion.so:8443/" + pageA
	var got []string
	for _, r := range Discover(text, []Resolver{Notion{}}, nil) {
		got = append(got, r.Kind+" "+r.ID+" "+r.URL)
	}
	want := []string{
		"page " + dashed(pageA) + " https://www.notion.so/acme/Launch-plan-" + pageA,
		"page " + dashed(pageB) + " https://app.notion.com/p/" + dashed(pageB),
		"page 11111111-1111-1111-1111-111111111111 https://acme.notion.site/Public-11111111111111111111111111111111",
		"page 22222222-2222-2222-2222-222222222222 https://www.notion.so/acme/Board-" + pageB + "?p=22222222222222222222222222222222",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("refs:\n%s", strings.Join(got, "\n"))
	}
	if _, ok := (Notion{}).ParseIdentifier("../x"); ok {
		t.Fatal("unsafe identifier accepted")
	}
}

func TestNotionDirectPage(t *testing.T) {
	svc, _, _, _ := newNotionHarness(t)
	p := notionResolve(svc, "alice", "see https://acme.notion.site/x-"+pageA)[0]
	if p.State != StateOK || p.Title != "📘 ABC-42 Launch plan" || p.Icon != "bi-file-earmark-text" ||
		p.URL != "https://www.notion.so/ABC-42-Launch-plan-"+pageA || p.UpdatedAt.IsZero() {
		t.Fatalf("page = %+v", p)
	}
}

func TestNotionTicketLookup(t *testing.T) {
	svc, api, _, _ := newNotionHarness(t)
	ps := notionResolve(svc, "alice", "ABC-42, ABC-7, ABC-9 and ABC-1")
	if len(ps) != 4 {
		t.Fatalf("previews = %+v", ps)
	}
	// Unique match: ABC-420 is not a token match for ABC-42.
	if p := ps[0]; p.State != StateOK || p.ID != "ABC-42" || p.Title != "📘 ABC-42 Launch plan" ||
		p.URL != "https://www.notion.so/ABC-42-Launch-plan-"+pageA {
		t.Fatalf("unique = %+v", p)
	}
	// Ambiguous: a chooser with API-returned URLs, no preview URL.
	if p := ps[1]; p.State != StateAmbiguous || p.URL != "" || len(p.Choices) != 2 ||
		p.Choices[0].URL != "https://www.notion.so/ABC-7-Retro-"+pageB || p.Choices[1].Title != "📘 ABC-7 follow-up" {
		t.Fatalf("ambiguous = %+v", p)
	}
	// Trashed and absent: not found, no invented link.
	for _, p := range ps[2:] {
		if p.State != StateNotFound || p.URL != "" || p.Choices != nil {
			t.Fatalf("missing = %+v", p)
		}
	}
	for _, c := range api.calls {
		if !strings.HasSuffix(c, "POST /v1/search") {
			t.Fatalf("unexpected call %s", c)
		}
	}
}

func TestNotionMissingAndRevokedPage(t *testing.T) {
	svc, api, tokens, _ := newNotionHarness(t)
	link := "https://www.notion.so/" + pageA
	if p := notionResolve(svc, "alice", "https://www.notion.so/"+strings.Repeat("9", 32))[0]; p.State != StateNotFound || p.Title != "" {
		t.Fatalf("missing = %+v", p)
	}
	if p := notionResolve(svc, "alice", link)[0]; p.State != StateOK {
		t.Fatalf("alice = %+v", p)
	}
	// Page unshared from the connection: Notion answers 404, served once
	// the cached entry expires.
	api.mu.Lock()
	api.shared["tok-alice"] = api.shared["tok-alice"][1:]
	api.mu.Unlock()
	svc.Purge("", "", "")
	if p := notionResolve(svc, "alice", link)[0]; p.State != StateNotFound || p.Title != "" {
		t.Fatalf("unshared = %+v", p)
	}
	// Connection removed in Notion: 401 forgets the grant.
	if p := notionResolve(svc, "eve", link)[0]; p.State != StateConnect {
		t.Fatalf("eve = %+v", p)
	}
	if strings.Join(tokens.revoked, ",") != "eve/W1" {
		t.Fatalf("revoked = %v", tokens.revoked)
	}
}

func TestNotionViewerIsolation(t *testing.T) {
	svc, api, _, _ := newNotionHarness(t)
	link := "https://www.notion.so/" + pageA
	if p := notionResolve(svc, "alice", link+" ABC-7")[0]; p.State != StateOK {
		t.Fatalf("alice = %+v", p)
	}
	api.mu.Lock()
	before := len(api.calls)
	api.mu.Unlock()
	// Bob's connection has not been given page A, and sees one ABC-7.
	bob := notionResolve(svc, "bob", link+" ABC-7")
	if bob[0].State != StateNotFound || bob[0].Title != "" || bob[1].State != StateOK || bob[1].Choices != nil {
		t.Fatalf("bob = %+v", bob)
	}
	if p := notionResolve(svc, "mallory", link)[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("mallory = %+v", p)
	}
	// Only bob's own token was used after alice.
	for _, c := range api.calls[before:] {
		if !strings.HasPrefix(c, "tok-bob ") {
			t.Fatalf("foreign token used: %s", c)
		}
	}
}

func TestNotionOAuth(t *testing.T) {
	p := NotionOAuth("cid", "secret", "")
	if p.AuthURL != "https://api.notion.com/v1/oauth/authorize" || p.AuthParams["owner"] != "user" ||
		!p.BasicAuth || !p.JSONBody || p.Headers["Notion-Version"] != notionVersion {
		t.Fatalf("provider = %+v", p)
	}
	grants, err := p.Identify(context.Background(), nil, previewauth.Token{Raw: map[string]any{
		"workspace_id": "W1", "workspace_name": "Acme", "owner": map[string]any{"user": map[string]any{"name": "Alice"}},
	}})
	if err != nil || len(grants) != 1 || grants[0].WorkspaceID != "W1" || grants[0].WorkspaceName != "Acme" || grants[0].AccountName != "Alice" {
		t.Fatalf("grants = %+v %v", grants, err)
	}
	if _, err := p.Identify(context.Background(), nil, previewauth.Token{Raw: map[string]any{}}); err == nil {
		t.Fatal("token without workspace identified")
	}
	if _, err := notionToken(map[string]any{"object": "error", "code": "invalid_grant"}); !errors.Is(err, previewauth.ErrRevoked) {
		t.Fatalf("invalid_grant = %v", err)
	}
	if raw, err := notionToken(map[string]any{"access_token": "a"}); err != nil || raw["access_token"] != "a" {
		t.Fatalf("token = %v %v", raw, err)
	}
}
