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

func TestLookupPRAndIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/myproj/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Patch","state":"open","user":{"login":"alice"},"head":{"ref":"patch","repo":{"full_name":"alice/myproj"}},"base":{"repo":{"full_name":"alice/myproj"}}}`))
		case "/api/v1/repos/alice/myproj/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Bug","state":"open","user":{"login":"alice"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, "tok")
	pr, err := c.LookupPR(context.Background(), "alice/myproj", 7)
	if err != nil || pr.Number != 7 || pr.Branch != "patch" {
		t.Fatalf("LookupPR() = %+v, %v", pr, err)
	}
	issue, err := c.LookupIssue(context.Background(), "alice/myproj", 3)
	if err != nil || issue.Number != 3 || issue.Title != "Bug" {
		t.Fatalf("LookupIssue() = %+v, %v", issue, err)
	}
}

func TestLookupPRDeletedBranch(t *testing.T) {
	for _, tt := range []struct {
		name, ref, label, want string
	}{
		{"deleted", "refs/pull/604/head", "factory/epic-2", "factory/epic-2"},
		{"missing label", "refs/pull/604/head", "", "refs/pull/604/head"},
		{"existing branch", "factory/epic-2", "different", "factory/epic-2"},
		{"other pull ref", "refs/pull/605/head", "factory/epic-2", "refs/pull/605/head"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"number": 604, "state": "closed", "merged": true, "head": map[string]string{"ref": tt.ref, "label": tt.label, "sha": "abc123"}})
			}))
			defer srv.Close()
			pr, err := newTestClient(t, srv, "tok").LookupPR(t.Context(), "alice/myproj", 604)
			if err != nil || pr.Branch != tt.want || pr.HeadSHA != "abc123" || pr.Status != "merged" {
				t.Fatalf("LookupPR() = %+v, %v", pr, err)
			}
		})
	}
}

func TestConvertPRToDraft(t *testing.T) {
	title := `fix: handle "quoted" titles`
	draft := false
	patches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/alice/repo/pulls/7" || r.Header.Get("Authorization") != "token token" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Method == http.MethodPatch {
			patches++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["title"] != `WIP: fix: handle "quoted" titles` {
				t.Errorf("body = %#v, %v", body, err)
			}
			// Forgejo ignores unsupported edit fields such as draft.
			if edited, ok := body["title"].(string); ok {
				title = edited
				draft = strings.HasPrefix(title, "WIP: ")
			}
			w.WriteHeader(http.StatusCreated)
		} else if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "open", "title": title, "draft": draft})
	}))
	defer srv.Close()

	client := newTestClient(t, srv, "token")
	for range 2 {
		if err := client.ConvertPRToDraft(t.Context(), "alice/repo", 7); err != nil {
			t.Fatal(err)
		}
	}
	if patches != 1 {
		t.Fatalf("patches = %d; already-draft PR should be left alone", patches)
	}
}

func TestConvertPRToDraftRejectsFailedEdit(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
		status           int
	}{
		{name: "status", status: http.StatusForbidden, want: "status 403"},
		{name: "not draft", status: http.StatusOK, body: `{"draft":false}`, want: "not converted"},
		{name: "invalid response", status: http.StatusOK, body: `{`, want: "decoding edited pull"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"state":"open","title":"Fix bug"}`))
					return
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			err := newTestClient(t, srv, "token").ConvertPRToDraft(t.Context(), "alice/repo", 7)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestConvertPRToDraftRejectsFailedLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("must not edit after failed lookup: %s", r.Method)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if err := newTestClient(t, srv, "token").ConvertPRToDraft(t.Context(), "alice/repo", 7); err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("error = %v", err)
	}
}

func TestLookupIssueRejectsPullRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"number":3,"title":"PR","pull_request":{"url":"https://example/pr/3"}}`))
	}))
	defer srv.Close()
	if _, err := newTestClient(t, srv, "tok").LookupIssue(context.Background(), "alice/myproj", 3); err == nil {
		t.Fatal("LookupIssue accepted a pull request")
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
