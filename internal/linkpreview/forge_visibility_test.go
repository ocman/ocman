package linkpreview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Review finding (critical): the public-only fallback must re-check repository
// visibility live. A cached "public" answer must not authorize reading a new
// private resource with the owner's token after the repository turns private.
func TestPublicOnlyVisibilityIsNeverCached(t *testing.T) {
	var private atomic.Bool
	var issueReads atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			fmt.Fprintf(w, `{"private":%v}`, private.Load())
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/"):
			issueReads.Add(1)
			fmt.Fprint(w, `{"title":"Issue","state":"open","user":{"login":"a"}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	f := Forge{ID: "github", Host: "github.com", APIBase: srv.URL, OwnerToken: t.Name(), Connectable: true}
	svc := New(nil, srv.Client(), f)
	// No WithOwnerAccess: another viewer gets the public-only owner fallback.
	if p := svc.resolve(context.Background(), "", "local", Ref{Provider: "github", Kind: "issue", ID: "o/r#1"}); p.State != StateOK {
		t.Fatalf("public issue: %+v", p)
	}
	private.Store(true)
	p := svc.resolve(context.Background(), "", "local", Ref{Provider: "github", Kind: "issue", ID: "o/r#2"})
	if p.State != StateConnect || issueReads.Load() != 1 {
		t.Fatalf("private repo authorized by cached visibility: %+v issue reads=%d", p, issueReads.Load())
	}
}
