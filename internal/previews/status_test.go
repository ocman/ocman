package previews

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
)

func TestPreviewPRStatusTarget(t *testing.T) {
	approved := true
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reviews") {
			if approved {
				fmt.Fprint(w, `[{"id":1,"user":{"login":"a"},"state":"APPROVED"}]`)
			} else {
				fmt.Fprint(w, `[]`)
			}
		} else {
			fmt.Fprint(w, `{"mergeable":false}`)
		}
	}))
	defer api.Close()
	s := previewServer(t, newMockOAuth(t), "").withResolvers(linkpreview.Forge{ID: "github", Host: "github.com", APIBase: api.URL})
	s.Client = api.Client()
	b := newBrowser()
	for _, request := range []string{
		`{"text":"https://github.com/o/r/issues/1","prStatus":true}`,
		`{"text":"https://evil.example/o/r/pull/1","prStatus":true}`,
		`{"text":"https://github.com/o/r/pull/1 https://github.com/o/r/pull/2","prStatus":true}`,
		`{"text":"https://github.com/o/r/pull/1","prStatus":true,"checksSha":"abc123"}`,
	} {
		if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", request); rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid status target: %d %s", rr.Code, rr.Body.String())
		}
	}
	request := `{"text":"https://github.com/o/r/pull/1","prStatus":true}`
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", request); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"prStatus":{"mergeable":false,"approved":true}`) {
		t.Fatalf("status: %d %s", rr.Code, rr.Body.String())
	}
	approved = false
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", request); !strings.Contains(rr.Body.String(), `"approved":true`) {
		t.Fatal("status cache not reused")
	}
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"https://github.com/o/r/pull/1","prStatus":true,"refresh":true}`); !strings.Contains(rr.Body.String(), `"approved":false`) {
		t.Fatalf("status not refreshed: %s", rr.Body.String())
	}
}

// A refresh whose review read is rate limited must never present the cached
// approval as fresh: it is marked stale, which the sidebar refuses to act on.
func TestPreviewPRStatusRefreshRejectsStaleApproval(t *testing.T) {
	limited := false
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reviews") {
			if limited {
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			fmt.Fprint(w, `[{"id":1,"user":{"login":"a"},"state":"APPROVED"}]`)
			return
		}
		fmt.Fprint(w, `{"mergeable":true}`)
	}))
	defer api.Close()
	s := newHarness(t).withResolvers(linkpreview.Forge{ID: "github", Host: "github.com", APIBase: api.URL, OwnerToken: "owner"})
	s.Client = api.Client()
	b := newBrowser()
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"https://github.com/o/r/pull/1","prStatus":true}`); !strings.Contains(rr.Body.String(), `"approved":true`) {
		t.Fatalf("warm: %s", rr.Body.String())
	}
	limited = true
	rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"https://github.com/o/r/pull/1","prStatus":true,"refresh":true}`)
	var resp struct{ Previews []linkpreview.Preview }
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp.Previews) != 1 {
		t.Fatalf("refresh: %s", rr.Body.String())
	}
	if p := resp.Previews[0]; p.PRStatus != nil && p.PRStatus.Approved && !p.Stale {
		t.Fatalf("stale approval served: %s", rr.Body.String())
	}
}
