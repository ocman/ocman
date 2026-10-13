package filesystem

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
)

type failingRepo struct{ err error }

func (f failingRepo) ListRepoFiles(context.Context, string, bool) (*git.FileList, error) {
	return nil, f.err
}

func (f failingRepo) ReadRepoFile(context.Context, string, string, bool) (*git.FileContent, error) {
	return nil, f.err
}

// An owner failure other than "not a repo"/"not listed" (e.g. a remote RPC
// error) is a 502 without leaking the error text.
func TestRepoFileHandlersMapOwnerErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errors.New("rpc: secret detail"), http.StatusBadGateway},
		{fmt.Errorf("wrapped: %w", git.ErrNotARepo), http.StatusNotFound},
		{git.ErrFileNotFound, http.StatusNotFound},
	} {
		m := NewModule(Deps{Owner: func(http.ResponseWriter, *http.Request) (string, RepoFiles, bool) {
			return "/repo", failingRepo{tc.err}, true
		}})
		for name, h := range map[string]http.HandlerFunc{"list": m.handleRepoFiles, "read": m.handleRepoFile} {
			rr := httptest.NewRecorder()
			h(rr, httptest.NewRequest(http.MethodGet, "/?path=a.go", nil))
			if rr.Code != tc.want {
				t.Errorf("%s %v: status = %d, want %d", name, tc.err, rr.Code, tc.want)
			}
			if tc.want == http.StatusBadGateway && rr.Body.String() == "" || strings.Contains(rr.Body.String(), "secret") {
				t.Errorf("%s: body = %q", name, rr.Body.String())
			}
		}
	}
}

// The owner seam writes its own error and stops the handler.
func TestRepoFileHandlersStopWhenOwnerRefuses(t *testing.T) {
	m := NewModule(Deps{Owner: func(w http.ResponseWriter, _ *http.Request) (string, RepoFiles, bool) {
		http.Error(w, "remote not connected", http.StatusServiceUnavailable)
		return "", nil, false
	}})
	for _, h := range []http.HandlerFunc{m.handleRepoFiles, m.handleRepoFile} {
		rr := httptest.NewRecorder()
		h(rr, httptest.NewRequest(http.MethodGet, "/?path=a.go", nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", rr.Code)
		}
	}
}
