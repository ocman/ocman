package forgehttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
)

func TestReviewApproval(t *testing.T) {
	for _, tc := range []struct {
		name, reviews string
		want          bool
	}{
		{"empty", `[]`, false},
		{"approved", `[{"id":1,"user":{"login":"a"},"state":"APPROVED"}]`, true},
		{"forgejo lowercase", `[{"id":1,"user":{"login":"a"},"state":"approved"}]`, true},
		{"dismissed flag", `[{"id":1,"user":{"login":"a"},"state":"APPROVED","dismissed":true}]`, false},
		{"dismissed state", `[{"id":1,"user":{"login":"a"},"state":"DISMISSED"}]`, false},
		{"blocking reviewer", `[{"id":1,"user":{"login":"a"},"state":"APPROVED"},{"id":2,"user":{"login":"b"},"state":"CHANGES_REQUESTED"}]`, false},
		{"forgejo blocking reviewer", `[{"id":1,"user":{"login":"a"},"state":"APPROVED"},{"id":2,"user":{"login":"b"},"state":"REQUEST_CHANGES"}]`, false},
		{"approval superseded", `[{"id":1,"user":{"login":"a"},"state":"APPROVED"},{"id":2,"user":{"login":"a"},"state":"CHANGES_REQUESTED"}]`, false},
		{"changes resolved unordered", `[{"id":2,"user":{"login":"a"},"state":"APPROVED"},{"id":1,"user":{"login":"a"},"state":"CHANGES_REQUESTED"}]`, true},
		{"comment and pending preserve approval", `[{"id":1,"user":{"login":"a"},"state":"APPROVED"},{"id":2,"user":{"login":"a"},"state":"COMMENTED"},{"id":3,"user":{"login":"a"},"state":"PENDING"}]`, true},
		{"no reviewer", `[{"id":1,"state":"APPROVED"}]`, false},
		{"submission order beats draft id", `[{"id":2,"user":{"login":"a"},"state":"CHANGES_REQUESTED","submitted_at":"2026-01-01T00:00:00Z"},{"id":1,"user":{"login":"a"},"state":"APPROVED","submitted_at":"2026-01-02T00:00:00Z"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetch := func(context.Context, string) ([]byte, forge.RateLimit, int, error) {
				return []byte(tc.reviews), forge.RateLimit{}, http.StatusOK, nil
			}
			got, err := ReviewApproval(context.Background(), fetch, "/reviews", "limit")
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestReviewApprovalPagination(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, path string) ([]byte, forge.RateLimit, int, error) {
		calls++
		if want := fmt.Sprintf("/reviews?per_page=30&page=%d", calls); path != want {
			t.Fatalf("path=%s want=%s", path, want)
		}
		body := `[{"id":31,"user":{"login":"a"},"state":"CHANGES_REQUESTED"}]`
		if calls == 1 {
			body = `[{"id":1,"user":{"login":"a"},"state":"APPROVED"},` + strings.Repeat(`{"state":"COMMENTED"},`, 28) + `{"state":"COMMENTED"}]`
		}
		return []byte(body), forge.RateLimit{}, http.StatusOK, nil
	}
	got, err := ReviewApproval(context.Background(), fetch, "/reviews", "per_page")
	if err != nil || got || calls != 2 {
		t.Fatalf("got %v, %v, calls=%d", got, err, calls)
	}
}

func TestReviewApprovalErrors(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		err    error
	}{
		{"", 0, errors.New("offline")},
		{"[]", http.StatusTooManyRequests, nil},
		{"not json", http.StatusOK, nil},
	} {
		fetch := func(context.Context, string) ([]byte, forge.RateLimit, int, error) {
			return []byte(tc.body), forge.RateLimit{Limited: true}, tc.status, tc.err
		}
		if got, err := ReviewApproval(context.Background(), fetch, "/reviews", "limit"); got || err == nil {
			t.Fatalf("got %v, %v", got, err)
		}
	}
}
