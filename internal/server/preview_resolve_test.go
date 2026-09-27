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
