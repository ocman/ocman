package forgejo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// newTestClient builds a Client pointed at the given httptest.Server.
// Uses the server's URL as the Forgejo "base URL"; the client appends
// /api/v1/... internally.
func newTestClient(t *testing.T, srv *httptest.Server, token string) *Client {
	t.Helper()
	return &Client{
		baseURL: srv.URL,
		host:    "test.forgejo",
		token:   token,
		http:    srv.Client(),
	}
}

func TestListPRs_ParsesAndMapsFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/repos/alice/myproj/pulls"; got != want {
			t.Errorf("path: got %s want %s", got, want)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state: %q", r.URL.Query().Get("state"))
		}
		_, _ = w.Write([]byte(`[
			{
				"number": 7,
				"title": "Patch",
				"body": "body text",
				"state": "open",
				"draft": false,
				"merged": false,
				"updated_at": "2026-05-21T14:03:11Z",
				"html_url": "https://test.forgejo/alice/myproj/pulls/7",
				"user": {"login": "alice", "avatar_url": "https://example/a.png"},
				"labels": [{"name": "infra", "color": "fef2c0"}],
				"assignees": [{"login": "alice"}],
				"requested_reviewers": [{"login": "bob"}],
				"head": {"ref": "patch", "repo": {"full_name": "alice/myproj"}},
				"base": {"repo": {"full_name": "alice/myproj"}}
			},
			{
				"number": 8,
				"title": "WIP",
				"body": "",
				"state": "open",
				"draft": true,
				"merged": false,
				"updated_at": "2026-05-22T09:00:00Z",
				"html_url": "https://test.forgejo/alice/myproj/pulls/8",
				"user": {"login": "carol"},
				"labels": [],
				"assignees": [],
				"requested_reviewers": [],
				"head": {"ref": "wip", "repo": {"full_name": "carol/myproj-fork"}},
				"base": {"repo": {"full_name": "alice/myproj"}}
			}
		]`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	prs, _, err := c.ListPRs(context.Background(), "alice/myproj", forge.ListOptions{})
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("expected 2 prs, got %d", len(prs))
	}

	want := forge.PR{
		Number:             7,
		Title:              "Patch",
		Body:               "body text",
		Author:             "alice",
		Status:             "open",
		UpdatedAt:          time.Date(2026, 5, 21, 14, 3, 11, 0, time.UTC),
		Labels:             []forge.Label{{Name: "infra", Color: "fef2c0"}},
		Assignees:          []forge.User{{Login: "alice"}},
		RequestedReviewers: []forge.User{{Login: "bob"}},
		Branch:             "patch",
		URL:                "https://test.forgejo/alice/myproj/pulls/7",
		Host:               "test.forgejo",
		Repo:               "alice/myproj",
		CrossFork:          false,
	}
	if !prEqual(prs[0], want) {
		t.Errorf("PR mismatch.\n got: %+v\nwant: %+v", prs[0], want)
	}

	if prs[1].Status != "draft" {
		t.Errorf("expected draft, got %q", prs[1].Status)
	}
	if !prs[1].CrossFork {
		t.Errorf("expected CrossFork=true")
	}
}

func TestListPRs_MergedStateMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"number": 1, "title": "merged",
			 "state": "closed", "merged": true,
			 "updated_at": "2026-05-01T12:00:00Z",
			 "html_url": "", "user": {"login": "x"},
			 "head": {"ref": "b", "repo": {"full_name": "o/r"}},
			 "base": {"repo": {"full_name": "o/r"}}},
			{"number": 2, "title": "closed-not-merged",
			 "state": "closed", "merged": false,
			 "updated_at": "2026-05-01T12:00:00Z",
			 "html_url": "", "user": {"login": "x"},
			 "head": {"ref": "b2", "repo": {"full_name": "o/r"}},
			 "base": {"repo": {"full_name": "o/r"}}}
		]`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "")
	prs, _, err := c.ListPRs(context.Background(), "o/r", forge.ListOptions{State: "closed"})
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if prs[0].Status != "merged" {
		t.Errorf("expected merged, got %q", prs[0].Status)
	}
	if prs[1].Status != "closed" {
		t.Errorf("expected closed, got %q", prs[1].Status)
	}
}

func TestListIssues_ExcludesPullRequestsByType(t *testing.T) {
	// Forgejo/Gitea's /issues endpoint also returns PRs by default.
	// The forgejo adapter requests type=issues to scope the result,
	// but we still defensively skip rows that have pull_request set.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("type"); got != "issues" {
			t.Errorf("type query: got %q want issues", got)
		}
		_, _ = w.Write([]byte(`[
			{"number": 9, "title": "Real issue", "body": "",
			 "state": "open",
			 "updated_at": "2026-05-21T14:03:11Z",
			 "html_url": "https://test.forgejo/alice/myproj/issues/9",
			 "user": {"login": "alice"},
			 "labels": [], "assignees": []},
			{"number": 10, "title": "Bleed through PR",
			 "state": "open",
			 "updated_at": "2026-05-21T14:03:11Z",
			 "html_url": "", "user": {"login": "alice"},
			 "labels": [], "assignees": [],
			 "pull_request": {"merged": false}}
		]`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	issues, _, err := c.ListIssues(context.Background(), "alice/myproj", forge.ListOptions{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 9 {
		t.Errorf("expected 1 issue #9, got %+v", issues)
	}
}

func TestCurrentUser_Authenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user" {
			t.Errorf("path: %s", r.URL.Path)
		}
		// Forgejo accepts "token <t>" and "Authorization: token <t>".
		// We use the latter to stay aligned with GitHub's Bearer style.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "token ") && !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("missing Authorization header (got %q)", auth)
		}
		_, _ = w.Write([]byte(`{"login": "alice"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	u, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if u.Login != "alice" || u.Host != "test.forgejo" {
		t.Errorf("got %+v", u)
	}
}

func TestCurrentUser_UnauthenticatedReturnsErr(t *testing.T) {
	c := &Client{token: "", baseURL: "http://unused", host: "test.forgejo"}
	_, err := c.CurrentUser(context.Background())
	if !errors.Is(err, forge.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestRegistry_RoutesByHost(t *testing.T) {
	a := &Client{host: "code.example.com"}
	b := &Client{host: "codeberg.org"}
	reg := &Registry{clients: map[string]*Client{
		"code.example.com": a,
		"codeberg.org":     b,
	}}

	if reg.ForHost("code.example.com") != a {
		t.Errorf("ForHost code.example.com: got %v want %v", reg.ForHost("code.example.com"), a)
	}
	if reg.ForHost("codeberg.org") != b {
		t.Errorf("ForHost codeberg.org: got %v want %v", reg.ForHost("codeberg.org"), b)
	}
	if reg.ForHost("github.com") != nil {
		t.Errorf("ForHost github.com should be nil")
	}
	if !reg.Knows("code.example.com") {
		t.Errorf("Knows should be true")
	}
	if reg.Knows("github.com") {
		t.Errorf("Knows github.com should be false")
	}
}

func TestGetPR_ReturnsRawMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/repos/alice/myproj/pulls/7"; got != want {
			t.Errorf("path: got %s want %s", got, want)
		}
		if got := r.Header.Get("Authorization"); got != "token tok" {
			t.Errorf("auth header: %q", got)
		}
		_, _ = w.Write([]byte(`{"number":7,"title":"Patch","state":"open","html_url":"https://test.forgejo/alice/myproj/pulls/7"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	data, err := c.GetPR(context.Background(), "alice", "myproj", 7)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if data["title"] != "Patch" || data["state"] != "open" {
		t.Errorf("unexpected payload: %+v", data)
	}
}

func TestGetIssue_ReturnsRawMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/repos/alice/myproj/issues/3"; got != want {
			t.Errorf("path: got %s want %s", got, want)
		}
		_, _ = w.Write([]byte(`{"number":3,"title":"Bug","state":"closed"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	data, err := c.GetIssue(context.Background(), "alice", "myproj", 3)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if data["title"] != "Bug" || data["state"] != "closed" {
		t.Errorf("unexpected payload: %+v", data)
	}
}

func TestGetCommit_ReturnsRawMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/repos/alice/myproj/git/commits/abc1234"; got != want {
			t.Errorf("path: got %s want %s", got, want)
		}
		_, _ = w.Write([]byte(`{"sha":"abc1234","commit":{"message":"do thing","author":{"name":"Alice"}}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	data, err := c.GetCommit(context.Background(), "alice", "myproj", "abc1234")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if data["sha"] != "abc1234" {
		t.Errorf("unexpected payload: %+v", data)
	}
}

func TestGetPR_Non200ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	if _, err := c.GetPR(context.Background(), "alice", "myproj", 999); err == nil {
		t.Fatalf("expected error on 404")
	}
}

// --- helpers ---

func prEqual(a, b forge.PR) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
