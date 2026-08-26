package github

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

// newTestClient returns a Client whose http client is pointed at the
// given test server. Sets the apiBase override so requests stay
// in-process.
func newTestClient(t *testing.T, srv *httptest.Server, token string) *Client {
	t.Helper()
	return &Client{
		token:   token,
		http:    srv.Client(),
		apiBase: srv.URL,
	}
}

func TestListPRs_ParsesAndMapsFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/alice/myproj/pulls"; got != want {
			t.Errorf("path: got %s want %s", got, want)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state query: got %q", r.URL.Query().Get("state"))
		}
		if r.URL.Query().Get("per_page") != "30" {
			t.Errorf("per_page query: got %q", r.URL.Query().Get("per_page"))
		}
		if r.URL.Query().Get("page") != "1" {
			t.Errorf("page query: got %q", r.URL.Query().Get("page"))
		}
		// Two PRs: one draft (open + draft:true => status "draft"),
		// one ready (open + draft:false => status "open").
		// Plus a cross-fork case: head.repo.full_name != base.repo.full_name.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"number": 42,
				"title": "Tighten slug rules",
				"body": "Description here.",
				"state": "open",
				"draft": false,
				"updated_at": "2026-05-21T14:03:11Z",
				"html_url": "https://github.com/alice/myproj/pull/42",
				"user": {"login": "alice", "avatar_url": "https://example/a.png"},
				"labels": [{"name": "infra", "color": "fef2c0"}],
				"assignees": [{"login": "alice", "avatar_url": "https://example/a.png"}],
				"requested_reviewers": [{"login": "bob", "avatar_url": "https://example/b.png"}],
				"head": {"ref": "tighten-slug", "repo": {"full_name": "alice/myproj"}},
				"base": {"repo": {"full_name": "alice/myproj"}}
			},
			{
				"number": 43,
				"title": "WIP fork PR",
				"body": "",
				"state": "open",
				"draft": true,
				"updated_at": "2026-05-22T09:00:00Z",
				"html_url": "https://github.com/alice/myproj/pull/43",
				"user": {"login": "carol", "avatar_url": "https://example/c.png"},
				"labels": [],
				"assignees": [],
				"requested_reviewers": [],
				"head": {"ref": "wip", "repo": {"full_name": "carol/myproj-fork"}},
				"base": {"repo": {"full_name": "alice/myproj"}}
			}
		]`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "token123")
	prs, _, err := c.ListPRs(context.Background(), "alice/myproj", forge.ListOptions{})
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("expected 2 prs, got %d", len(prs))
	}

	got := prs[0]
	want := forge.PR{
		Number:             42,
		Title:              "Tighten slug rules",
		Body:               "Description here.",
		Author:             "alice",
		Status:             "open",
		UpdatedAt:          time.Date(2026, 5, 21, 14, 3, 11, 0, time.UTC),
		Labels:             []forge.Label{{Name: "infra", Color: "fef2c0"}},
		Assignees:          []forge.User{{Login: "alice", AvatarURL: "https://example/a.png"}},
		RequestedReviewers: []forge.User{{Login: "bob", AvatarURL: "https://example/b.png"}},
		Branch:             "tighten-slug",
		URL:                "https://github.com/alice/myproj/pull/42",
		Host:               "github.com",
		Repo:               "alice/myproj",
		CrossFork:          false,
	}
	if !prEqual(got, want) {
		t.Errorf("PR mismatch.\n got: %+v\nwant: %+v", got, want)
	}

	if prs[1].Status != "draft" {
		t.Errorf("expected draft status, got %q", prs[1].Status)
	}
	if !prs[1].CrossFork {
		t.Errorf("expected CrossFork=true for fork PR")
	}
}

func TestLookupIssueRejectsPullRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"number":7,"title":"PR","pull_request":{"url":"https://api.example/pr/7"}}`))
	}))
	defer srv.Close()
	c := NewForTest(srv.URL, "token", srv.Client())
	if _, err := c.LookupIssue(context.Background(), "alice/repo", 7); err == nil {
		t.Fatal("LookupIssue accepted a pull request")
	}
}

func TestListPRs_StateMergedDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{
				"number": 1, "title": "merged", "state": "closed",
				"merged_at": "2026-05-01T12:00:00Z",
				"updated_at": "2026-05-01T12:00:00Z",
				"html_url": "", "user": {"login": "x"},
				"head": {"ref": "b", "repo": {"full_name": "o/r"}},
				"base": {"repo": {"full_name": "o/r"}}
			},
			{
				"number": 2, "title": "closed-not-merged", "state": "closed",
				"updated_at": "2026-05-01T12:00:00Z",
				"html_url": "", "user": {"login": "x"},
				"head": {"ref": "b2", "repo": {"full_name": "o/r"}},
				"base": {"repo": {"full_name": "o/r"}}
			}
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

func TestListIssues_SkipsPRsInIssuesEndpoint(t *testing.T) {
	// GitHub's /issues endpoint returns BOTH issues and PRs; PRs
	// are distinguishable by a non-null pull_request field.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/alice/myproj/issues"; got != want {
			t.Errorf("path: got %s want %s", got, want)
		}
		_, _ = w.Write([]byte(`[
			{
				"number": 7, "title": "Real issue", "body": "...",
				"state": "open",
				"updated_at": "2026-05-21T14:03:11Z",
				"html_url": "https://github.com/alice/myproj/issues/7",
				"user": {"login": "alice"},
				"labels": [], "assignees": []
			},
			{
				"number": 42, "title": "Tighten slug rules", "state": "open",
				"updated_at": "2026-05-21T14:03:11Z",
				"html_url": "https://github.com/alice/myproj/pull/42",
				"user": {"login": "alice"},
				"labels": [], "assignees": [],
				"pull_request": {"url": "..."}
			}
		]`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "")
	issues, _, err := c.ListIssues(context.Background(), "alice/myproj", forge.ListOptions{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (PR filtered), got %d", len(issues))
	}
	if issues[0].Number != 7 {
		t.Errorf("expected issue #7, got #%d", issues[0].Number)
	}
}

func TestListPRs_RateLimitedReturnsFlag(t *testing.T) {
	resetTime := time.Now().Add(2 * time.Minute).UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Reset", itoa(resetTime.Unix()))
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	prs, rl, err := c.ListPRs(context.Background(), "o/r", forge.ListOptions{})
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(prs) != 0 {
		t.Errorf("expected empty list on rate limit, got %d", len(prs))
	}
	if !rl.Limited {
		t.Errorf("expected RateLimit.Limited=true")
	}
	if rl.ResetAt.IsZero() {
		t.Errorf("expected RateLimit.ResetAt to be set")
	}
}

func TestCurrentUser_Authenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing/invalid Authorization header")
		}
		_, _ = w.Write([]byte(`{"login": "alice"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	u, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if u.Login != "alice" || u.Host != "github.com" {
		t.Errorf("got %+v", u)
	}
}

func TestCurrentUser_UnauthenticatedReturnsErr(t *testing.T) {
	c := &Client{token: "", apiBase: "http://unused"}
	_, err := c.CurrentUser(context.Background())
	if err == nil || !errors.Is(err, forge.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
}

// --- helpers ---

func prEqual(a, b forge.PR) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func itoa(n int64) string {
	return strings.TrimSpace((func() string {
		buf := make([]byte, 0, 20)
		if n == 0 {
			return "0"
		}
		neg := n < 0
		if neg {
			n = -n
		}
		for n > 0 {
			buf = append([]byte{byte('0' + n%10)}, buf...)
			n /= 10
		}
		if neg {
			buf = append([]byte{'-'}, buf...)
		}
		return string(buf)
	})())
}
