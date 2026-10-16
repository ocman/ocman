package previews

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
)

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
			if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", body); rr.Code != 200 || !strings.Contains(rr.Body.String(), sha) {
				t.Fatalf("render reused old metadata: %d %s", rr.Code, rr.Body.String())
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
	// Only PR metadata is revalidated; the issue remains cached.
	if calls.Load() != 3 {
		t.Fatalf("fetches = %d, want 3", calls.Load())
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
