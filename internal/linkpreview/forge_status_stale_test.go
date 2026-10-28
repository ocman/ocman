package linkpreview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/forge/github"
)

func TestPreviewStatusRejectsStaleReviewsAfterFreshMetadata(t *testing.T) {
	limited := false
	metadataReads := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reviews") {
			if limited {
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			fmt.Fprint(w, `[{"id":1,"user":{"login":"a"},"state":"APPROVED"}]`)
			return
		}
		metadataReads++
		fmt.Fprint(w, `{"mergeable":true}`)
	}))
	defer srv.Close()
	ctx := WithOwnerAccess(context.Background())
	client := github.NewWithCredentials(srv.URL, "owner", srv.Client())
	if approved, err := client.PRApproval(github.WithCache(ctx), "o/r", 42); err != nil || !approved {
		t.Fatalf("warm reviews: %v %v", approved, err)
	}
	limited = true
	svc := New(nil, srv.Client(), Forge{ID: "github", Host: "github.com", APIBase: srv.URL, OwnerToken: "owner"})
	p := svc.resolve(WithPreviewRefresh(ctx), "", "local", Ref{Provider: "github", Kind: "pr-status", ID: "o/r#42"})
	if metadataReads != 1 || p.State != StateRateLimited || p.PRStatus != nil {
		t.Fatalf("stale reviews became fresh status: %+v, metadata reads=%d", p, metadataReads)
	}
}
