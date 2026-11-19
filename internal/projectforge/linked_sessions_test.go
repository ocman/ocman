package projectforge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
)

type sessionListForge struct{ forge.Forge }

func (sessionListForge) ListPRs(context.Context, string, forge.ListOptions) ([]forge.PR, forge.RateLimit, error) {
	return []forge.PR{{Number: 42, Title: "Linked"}, {Number: 43, Title: "Unlinked"}}, forge.RateLimit{}, nil
}

func TestPRListIncludesLinkedSessions(t *testing.T) {
	m := NewModule(Deps{
		Owner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) {
			return &fakeHost{upstreams: githubUpstreams("/repo")}, true
		},
		ResolveForge: func(forge.Remote) (forge.Forge, bool) { return sessionListForge{}, true },
	})
	var lookupErr error
	m.PRSessions = func(_ context.Context, owner, host, repo string) (map[int][]SessionRef, error) {
		if owner != "box" || host != "github.com" || repo != "alice/myproj" {
			t.Fatalf("wrong scope: %s %s %s", owner, host, repo)
		}
		return map[int][]SessionRef{42: {{Platform: "r-box:opencode", SessionID: "s1"}, {Platform: "r-box:claude", SessionID: "s2"}}}, lookupErr
	}
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/project/prs?dir=/repo&remoteId=box&remote=origin", nil))
		return w
	}
	w := get()
	var result struct {
		PRs []struct {
			Number   int
			Sessions []SessionRef
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || len(result.PRs) != 2 {
		t.Fatalf("response=%d %s err=%v", w.Code, w.Body, err)
	}
	if len(result.PRs[0].Sessions) != 2 || result.PRs[0].Sessions[0].Platform != "r-box:opencode" || len(result.PRs[1].Sessions) != 0 {
		t.Fatalf("PRs=%+v", result.PRs)
	}
	lookupErr = errors.New("owner offline")
	if w = get(); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup failure=%d %s", w.Code, w.Body)
	}
}
