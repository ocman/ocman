package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/previewauth"
)

type catalogResp struct {
	Providers []previewProviderView
	Rules     []string
	HostKinds []previewHostKind
}

func catalogFull(t *testing.T, s *Server) (catalogResp, string) {
	t.Helper()
	body := statusOf(t, newBrowser(), s)
	var resp catalogResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func catalogOf(t *testing.T, s *Server) []previewProviderView {
	resp, _ := catalogFull(t, s)
	return resp.Providers
}

func entryOf(t *testing.T, s *Server, id string) previewProviderView {
	t.Helper()
	for _, e := range catalogOf(t, s) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no catalog entry %s", id)
	return previewProviderView{}
}

// The browser learns what is supported and configured, and the link hosts
// to look for, never a credential.
func TestPreviewProviders_Catalog(t *testing.T) {
	s := testServer(t)
	s.integrations.GitHub = github.NewForTest("https://api.github.com", "tok-cli", nil)
	registerForgejoClient(s, "tea.example.com", "https://tea.example.com")
	b := newBrowser()
	rules := `{"rules":[{"pattern":"ENG-\\d+","replacement":"https://linear.app/x/issue/$&","provider":"linear"}]}`
	if rr := b.do(t, s, http.MethodPost, "/api/settings/link-preview-rules", rules); rr.Code != http.StatusOK {
		t.Fatalf("rules = %d", rr.Code)
	}
	resp, body := catalogFull(t, s)
	if strings.Contains(body, "tok-cli") || strings.Contains(body, `"tok"`) {
		t.Fatalf("catalog leaks a token: %s", body)
	}
	got := map[string]previewProviderView{}
	for _, p := range resp.Providers {
		got[p.ID] = p
	}
	for id, want := range map[string]struct {
		configured bool
		source     string
		host       string
	}{
		"github":                  {true, "cli", "github.com"},
		"forgejo:tea.example.com": {true, "cli", "tea.example.com"},
		"gitlab:gitlab.com":       {true, "public", "gitlab.com"},
		"linear":                  {false, "", "linear.app"},
		"notion":                  {false, "", "*.notion.site"},
	} {
		p := got[id]
		if p.Configured != want.configured || p.Source != want.source || !contains(p.Hosts, want.host) || !p.Token || p.TokenHelp == "" {
			t.Fatalf("%s = %+v", id, p)
		}
	}
	if _, ok := got["slack"]; ok {
		t.Fatal("slack listed without an app")
	}
	// A rule routed to an unconfigured provider would only cause useless calls.
	if len(resp.Rules) != 0 || len(resp.HostKinds) != 2 {
		t.Fatalf("rules = %v, host kinds = %v", resp.Rules, resp.HostKinds)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// A pasted token is checked with the provider, saved for the machine and
// makes the provider configured; rejected tokens are not stored.
func TestPreviewToken_SaveAndDisconnect(t *testing.T) {
	tokenProvider := previewauth.Provider{
		ID: "mock", Name: "Mock", TokenHelp: "paste it",
		Identify: func(_ context.Context, _ *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			if tok.AccessToken != "good-token" {
				return nil, previewauth.ErrExchange
			}
			return []previewauth.Grant{{WorkspaceID: "w1", WorkspaceName: "Acme", AccountName: "dries"}}, nil
		},
	}
	s := testServer(t).WithPreviewProviders(nil, tokenProvider, previewauth.Provider{ID: "oauth-only", Name: "OAuth only"})
	b := newBrowser()
	for body, want := range map[string]int{
		`{"provider":"mock","token":"bad"}`:           http.StatusBadRequest,
		`{"provider":"mock","token":"two words"}`:     http.StatusBadRequest,
		`{"provider":"oauth-only","token":"x"}`:       http.StatusBadRequest,
		`{"provider":"nope","token":"x"}`:             http.StatusNotFound,
		`{"provider":"forgejo:bad host","token":"x"}`: http.StatusNotFound,
		`{"provider":"mock","token":" good-token "}`:  http.StatusNoContent,
	} {
		if rr := b.do(t, s, http.MethodPost, "/api/previews/token", body); rr.Code != want {
			t.Fatalf("%s = %d %s", body, rr.Code, rr.Body.String())
		}
	}
	e := entryOf(t, s, "mock")
	if !e.Configured || len(e.Accounts) != 1 || e.Accounts[0].AccountName != "dries" {
		t.Fatalf("after save = %+v", e)
	}
	if rr := b.do(t, s, http.MethodPost, "/api/previews/disconnect", `{"provider":"mock"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("disconnect = %d", rr.Code)
	}
	if e := entryOf(t, s, "mock"); e.Configured || len(e.Accounts) != 0 {
		t.Fatalf("after disconnect = %+v", e)
	}
}

// A token for a new Forgejo host adds that host: its links resolve with the
// token, and removing it takes the host away again.
func TestPreviewToken_AddsForgejoHost(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pat-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/user":
			fmt.Fprint(w, `{"login":"dries"}`)
		case "/api/v1/repos/acme/priv/pulls/7":
			fmt.Fprint(w, `{"title":"Private PR","state":"open"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	host := strings.TrimPrefix(api.URL, "https://")
	s := testServer(t)
	s.previewAuth.client = api.Client()
	b := newBrowser()
	id := "forgejo:" + host
	if rr := b.do(t, s, http.MethodPost, "/api/previews/token", `{"provider":"`+id+`","token":"pat-1"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("save = %d %s", rr.Code, rr.Body.String())
	}
	if e := entryOf(t, s, id); !e.Configured || e.Accounts[0].AccountName != "dries" {
		t.Fatalf("entry = %+v", e)
	}
	link := (&url.URL{Scheme: "https", Host: host, Path: "/acme/priv/pulls/7"}).String()
	if got := resolvePreviews(t, b, s, link); len(got) != 1 || got[0].Title != "Private PR" {
		t.Fatalf("resolve = %+v", got)
	}
	b.do(t, s, http.MethodPost, "/api/previews/disconnect", `{"provider":"`+id+`"}`)
	for _, e := range catalogOf(t, s) {
		if e.ID == id {
			t.Fatalf("host kept after removing its token: %+v", e)
		}
	}
}
