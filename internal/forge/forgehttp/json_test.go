package forgehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
)

func fakeFetch(body string, status int, err error) Fetch {
	return func(context.Context, string) ([]byte, forge.RateLimit, int, error) {
		return []byte(body), forge.RateLimit{Limited: status == http.StatusTooManyRequests}, status, err
	}
}

func TestGetJSON(t *testing.T) {
	ctx := context.Background()
	got, _, err := GetJSON[map[string]int](ctx, fakeFetch(`{"a":1}`, http.StatusOK, nil), "/x", "x", UpstreamStatus)
	if err != nil || got["a"] != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, _, err := GetJSON[int](ctx, fakeFetch(`nope`, http.StatusOK, nil), "/x", "thing", UpstreamStatus); err == nil || !strings.HasPrefix(err.Error(), "decoding thing: ") {
		t.Fatalf("decode error = %v", err)
	}
	var re *ResponseError
	if _, _, err := GetJSON[int](ctx, fakeFetch(``, http.StatusNotFound, nil), "/x", "x", UpstreamStatus); !errors.As(err, &re) || re.Status != http.StatusNotFound {
		t.Fatalf("status error = %v", err)
	}
	boom := errors.New("boom")
	if _, _, err := GetJSON[int](ctx, fakeFetch(``, 0, boom), "/x", "x", UpstreamStatus); !errors.Is(err, boom) {
		t.Fatalf("fetch error = %v", err)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	keepEven := func(n int) (int, bool) { return n * 10, n%2 == 0 }
	got, _, err := List(ctx, fakeFetch(`[1,2,3,4]`, http.StatusOK, nil), "/x", "x", keepEven)
	if err != nil || !reflect.DeepEqual(got, []int{20, 40}) {
		t.Fatalf("got %v, %v", got, err)
	}
	got, rl, err := List(ctx, fakeFetch(``, http.StatusTooManyRequests, nil), "/x", "x", keepEven)
	if err != nil || got != nil || !rl.Limited {
		t.Fatalf("429: got %v, %+v, %v", got, rl, err)
	}
	var re *ResponseError
	if _, _, err := List(ctx, fakeFetch(``, http.StatusNotFound, nil), "/x", "x", keepEven); !errors.As(err, &re) {
		t.Fatalf("404: %v", err)
	}
}

func TestCurrentUser(t *testing.T) {
	ctx := context.Background()
	if _, err := CurrentUser(ctx, fakeFetch(``, http.StatusOK, nil), "h", "/user", ""); !errors.Is(err, forge.ErrUnauthenticated) {
		t.Fatalf("no token: %v", err)
	}
	got, err := CurrentUser(ctx, fakeFetch(`{"login":"me"}`, http.StatusOK, nil), "h", "/user", "t")
	if err != nil || got != (forge.CurrentUser{Host: "h", Login: "me"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		if _, err := CurrentUser(ctx, fakeFetch(``, status, nil), "h", "/user", "t"); !errors.Is(err, forge.ErrUnauthenticated) {
			t.Fatalf("%d: %v", status, err)
		}
	}
	var re *ResponseError
	if _, err := CurrentUser(ctx, fakeFetch(``, http.StatusNotFound, nil), "h", "/user", "t"); !errors.As(err, &re) {
		t.Fatalf("404: %v", err)
	}
}

func TestPRToForge(t *testing.T) {
	var raw PR
	if err := json.Unmarshal([]byte(`{
		"number": 7, "title": "T", "state": "closed", "html_url": "u",
		"user": {"login": "a"},
		"labels": [{"name": "bug", "color": "f00"}],
		"assignees": [{"login": "b", "avatar_url": "av"}],
		"requested_reviewers": [{"login": "c"}],
		"head": {"ref": "feat", "sha": "abc", "repo": {"full_name": "fork/r"}},
		"base": {"ref": "main", "repo": {"full_name": "o/r"}}
	}`), &raw); err != nil {
		t.Fatal(err)
	}
	pr := raw.ToForge("h", "o/r", true)
	want := forge.PR{
		Number: 7, Title: "T", Author: "a", Status: "merged", Branch: "feat", BaseBranch: "main",
		URL: "u", Host: "h", Repo: "o/r", HeadSHA: "abc", CrossFork: true,
		Labels:             []forge.Label{{Name: "bug", Color: "f00"}},
		Assignees:          []forge.User{{Login: "b", AvatarURL: "av"}},
		RequestedReviewers: []forge.User{{Login: "c"}},
	}
	if !reflect.DeepEqual(pr, want) {
		t.Fatalf("got %+v\nwant %+v", pr, want)
	}
	if got := raw.ToForge("h", "o/r", false).Status; got != "closed" {
		t.Fatalf("unmerged status = %q", got)
	}
	raw.State, raw.Draft = "open", true
	if got := raw.ToForge("h", "o/r", false).Status; got != "draft" {
		t.Fatalf("draft status = %q", got)
	}
	if got := (PR{}).ToForge("h", "o/r", false); got.Labels != nil || got.Assignees != nil || got.RequestedReviewers != nil {
		t.Fatalf("empty slices must stay nil: %+v", got)
	}
}

func TestIssueToForge(t *testing.T) {
	is := Issue{Number: 3, Title: "T", State: "open", User: User{Login: "a"}, Labels: []Label{{Name: "x"}}}.ToForge("h", "o/r")
	want := forge.Issue{Number: 3, Title: "T", Status: "open", Author: "a", Host: "h", Repo: "o/r", Labels: []forge.Label{{Name: "x"}}}
	if !reflect.DeepEqual(is, want) {
		t.Fatalf("got %+v", is)
	}
}
