package linkpreview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

func TestForgePreviewChecks(t *testing.T) {
	for _, gitea := range []bool{false, true} {
		t.Run(fmt.Sprint(gitea), func(t *testing.T) {
			limited, failed := false, false
			calls := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer explicit" && r.Header.Get("Authorization") != "token explicit" {
					t.Errorf("wrong credential: %s", r.Header.Get("Authorization"))
				}
				path := strings.TrimPrefix(r.URL.Path, "/api/v1")
				switch {
				case path == "/repos/o/private":
					fmt.Fprint(w, `{"private":true}`)
				case path == "/repos/o/r/pulls/1":
					fmt.Fprint(w, `{"title":"PR","head":{"sha":"abc123"}}`)
				case limited:
					w.WriteHeader(http.StatusTooManyRequests)
				case failed:
					w.WriteHeader(http.StatusInternalServerError)
				case strings.HasSuffix(path, "/status"):
					fmt.Fprint(w, `{"total_count":1,"statuses":[{"context":"build","state":"success","status":"success"}]}`)
				case strings.HasSuffix(path, "/check-runs"):
					fmt.Fprint(w, `{"total_count":1,"check_runs":[{"name":"test","status":"completed","conclusion":"success"}]}`)
				default:
					t.Errorf("unexpected request: %s", path)
				}
			}))
			defer srv.Close()
			base := srv.URL
			if gitea {
				base += "/api/v1"
			}
			f := Forge{ID: "github", Host: "github.com", APIBase: base, Gitea: gitea, OwnerToken: "explicit", Connectable: true}
			svc := New(nil, srv.Client(), f)
			ctx := WithOwnerAccess(context.Background())
			pr := svc.resolve(ctx, "", "local", Ref{Provider: "github", Kind: "pr", ID: "o/r#1"})
			if pr.HeadSHA != "abc123" {
				t.Fatalf("missing SHA: %+v", pr)
			}
			ref := Ref{Provider: "github", Kind: "checks", ID: "o/r@abc123"}
			p := svc.resolve(ctx, "", "local", ref)
			if p.Checks == nil || p.Checks.State != forge.CIStateSuccess {
				t.Fatalf("checks: %+v", p)
			}
			count := calls
			svc.resolve(ctx, "", "local", ref)
			if calls != count {
				t.Fatal("fresh checks were fetched twice")
			}
			svc.now = func() time.Time { return time.Now().Add(16 * time.Second) }
			limited = true
			p = svc.resolve(ctx, "", "local", ref)
			if !p.Stale {
				t.Fatalf("rate limit should mark cached result stale: %+v", p)
			}
			svc.Purge("", "", "")
			limited = false
			failed = true
			// New owner bypasses the previous grant's rate-limit backoff.
			p = svc.resolve(ctx, "", "other", ref)
			if p.State != StateError || p.Checks != nil {
				t.Fatalf("failed response: %+v", p)
			}
			failed = false
			p = svc.resolve(context.Background(), "", "local", Ref{Provider: "github", Kind: "checks", ID: "o/private@abc123"})
			if p.State != StateConnect || p.Checks != nil {
				t.Fatalf("private checks leaked: %+v", p)
			}
			p = svc.resolve(ctx, "", "other", Ref{Provider: "github", Kind: "checks", ID: "o/r@invalid"})
			if p.State != StateError {
				t.Fatalf("invalid SHA: %+v", p)
			}
		})
	}
}
