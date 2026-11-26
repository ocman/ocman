package previews

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/linkpreview"
)

func TestMergedPreviewRequiresExplicitRefresh(t *testing.T) {
	var calls atomic.Int32
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"title":"Version %d","state":"closed","merged":true}`, calls.Add(1))
	}))
	defer api.Close()
	s := previewServer(t, newMockOAuth(t), "").withResolvers(linkpreview.Forge{ID: "forgejo:forge.example", Host: "forge.example", APIBase: api.URL, Gitea: true})
	s.Client = api.Client()
	b := newBrowser()
	for _, step := range []struct{ options, title string }{
		{`,"render":true`, "Version 1"},
		{`,"render":true,"refresh":true`, "Version 1"},
		{`,"refresh":true`, "Version 2"},
		{`,"render":true`, "Version 2"},
	} {
		body := `{"text":"https://forge.example/o/r/pulls/1/files"` + step.options + `}`
		rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", body)
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), step.title) || !strings.Contains(rr.Body.String(), `"status":"Merged"`) {
			t.Fatalf("options %q: got %d %s, want %s", step.options, rr.Code, rr.Body.String(), step.title)
		}
	}
}

func TestConversationRenderRefresh(t *testing.T) {
	for _, gitea := range []bool{false, true} {
		t.Run(fmt.Sprint(gitea), func(t *testing.T) {
			sha := "abc123"
			api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"title":"PR","head":{"sha":%q}}`, sha)
			}))
			defer api.Close()
			id, path := "github", "pull"
			if gitea {
				id, path = "forgejo:forge.example", "pulls"
			}
			s := previewServer(t, newMockOAuth(t), "").withResolvers(linkpreview.Forge{ID: id, Host: "forge.example", APIBase: api.URL, Gitea: gitea})
			s.Client = api.Client()
			b := newBrowser()
			body := fmt.Sprintf(`{"text":"https://forge.example/o/r/%s/1","render":true}`, path)
			b.do(t, s, http.MethodPost, "/api/previews/resolve", body)
			sha = "def456"
			if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", body); rr.Code != 200 || !strings.Contains(rr.Body.String(), "abc123") {
				t.Fatalf("render did not reuse cached metadata: %d %s", rr.Code, rr.Body.String())
			}
			refresh := strings.TrimSuffix(body, "}") + `,"refresh":true}`
			if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", refresh); rr.Code != 200 || !strings.Contains(rr.Body.String(), sha) {
				t.Fatalf("refresh reused old open PR metadata: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestPreviewBatch(t *testing.T) {
	var calls atomic.Int32
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"title":%q}`, r.URL.Path)
	}))
	defer api.Close()
	s := previewServer(t, newMockOAuth(t), "").withResolvers(linkpreview.Forge{ID: "forgejo:forge.example", Host: "forge.example", APIBase: api.URL, Gitea: true})
	s.Client = api.Client()
	b := newBrowser()
	body := `{"requests":[{"text":"https://forge.example/o/r/pulls/1","render":true},{"text":"https://forge.example/o/r/issues/2","render":true},{"text":"https://forge.example/o/r/issues/2","prStatus":true}]}`
	for range 2 {
		rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", body)
		var result struct{ Results []previewResponse }
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if rr.Code != 200 || len(result.Results) != 3 || result.Results[0].Previews[0].Title != "/repos/o/r/pulls/1" || result.Results[1].Previews[0].Title != "/repos/o/r/issues/2" || result.Results[2].Error != "invalid PR status target" {
			t.Fatalf("batch: %d %s", rr.Code, rr.Body.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("fetches = %d, want 2", calls.Load())
	}
	for _, body := range []string{`{"requests":[]}`, `{"requests":[` + strings.Repeat(`{},`, 32) + `{}]}`} {
		if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", body); rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid batch: %d %s", rr.Code, rr.Body.String())
		}
	}
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve?remoteId=missing", body); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("disconnected batch: %d", rr.Code)
	}
}
