package linkpreview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge/github"
)

func TestForgePreviewStatus(t *testing.T) {
	for _, gitea := range []bool{false, true} {
		t.Run(fmt.Sprint(gitea), func(t *testing.T) {
			calls := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch strings.TrimPrefix(r.URL.Path, "/api/v1") {
				case "/repos/o/private":
					fmt.Fprint(w, `{"private":true}`)
				case "/repos/o/r/pulls/42":
					fmt.Fprint(w, `{"title":"PR","mergeable":false,"draft":true,"head":{"sha":"abc123"}}`)
				case "/repos/o/r/pulls/42/reviews":
					fmt.Fprint(w, `[{"id":1,"user":{"login":"reviewer"},"state":"APPROVED"}]`)
				case "/repos/o/r/pulls/43":
					fmt.Fprint(w, `{"mergeable":null}`)
				case "/repos/o/r/pulls/43/reviews":
					w.WriteHeader(http.StatusUnauthorized)
				default:
					t.Errorf("unexpected request: %s", r.URL)
				}
			}))
			defer srv.Close()
			base := srv.URL
			if gitea {
				base += "/api/v1"
			}
			f := Forge{ID: "github", Host: "github.com", APIBase: base, Gitea: gitea, OwnerToken: "token", Connectable: true}
			svc := New(nil, srv.Client(), f)
			ctx := WithOwnerAccess(context.Background())
			ref := Ref{Provider: "github", Kind: "pr-status", ID: "o/r#42"}
			p := svc.resolve(ctx, "", "local", ref)
			if p.State != StateOK || p.PRStatus == nil || p.PRStatus.Mergeable == nil || *p.PRStatus.Mergeable || !p.PRStatus.Approved {
				t.Fatalf("status: %+v", p)
			}
			count := calls
			svc.resolve(ctx, "", "local", ref)
			if calls != count {
				t.Fatal("resolved status fetched twice")
			}
			if !gitea {
				// The sidebar reads the identical credential-scoped REST cache.
				client := github.NewWithCredentials(base, "token", srv.Client())
				if _, err := client.LookupPR(github.WithCache(ctx), "o/r", 42); err != nil {
					t.Fatal(err)
				}
				if _, err := client.PRApproval(github.WithCache(ctx), "o/r", 42); err != nil {
					t.Fatal(err)
				}
				if calls != count {
					t.Fatal("sidebar did not reuse preview HTTP data")
				}
			}
			if draft := svc.resolve(ctx, "", "local", Ref{Provider: "github", Kind: "pr", ID: "o/r#42"}); draft.Status != "Draft" {
				t.Fatalf("draft status: %+v", draft)
			}
			if denied := svc.resolve(ctx, "", "local", Ref{Provider: "github", Kind: "pr-status", ID: "o/r#43"}); denied.State != StateError || denied.PRStatus != nil {
				t.Fatalf("failed review read: %+v", denied)
			}
			if private := svc.resolve(context.Background(), "", "public", Ref{Provider: "github", Kind: "pr-status", ID: "o/private#42"}); private.State != StateConnect || private.PRStatus != nil {
				t.Fatalf("private status leaked: %+v", private)
			}
			if invalid := svc.resolve(ctx, "", "local", Ref{Provider: "github", Kind: "pr-status", ID: "o/r@abc123"}); invalid.State != StateError {
				t.Fatalf("invalid status target: %+v", invalid)
			}
		})
	}
}
