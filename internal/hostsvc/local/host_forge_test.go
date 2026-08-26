package local

import (
	"context"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func TestProjectForgeOperationsUseInjectedOwnerCallbacks(t *testing.T) {
	want := &hostsvc.ProjectUpstreams{RepoRoot: "/owner/repo", Remotes: []forge.Remote{{Name: "origin", Host: "github.com", Repo: "owner/repo"}}}
	h := New(Deps{
		ProjectUpstreams: func(_ context.Context, dir string) (*hostsvc.ProjectUpstreams, error) {
			if dir != "/owner/repo/subdir" {
				t.Fatalf("dir = %q", dir)
			}
			return want, nil
		},
		FetchPRHead: func(_ context.Context, req hostsvc.FetchPRHeadRequest) (string, error) {
			if req.RepoRoot != "/owner/repo" || req.Remote != "origin" || req.Number != 42 {
				t.Fatalf("request = %+v", req)
			}
			return "ocman/pr-42", nil
		},
	})

	got, err := h.ProjectUpstreams(context.Background(), "/owner/repo/subdir")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ProjectUpstreams() = %+v, %v", got, err)
	}
	branch, err := h.FetchPRHead(context.Background(), hostsvc.FetchPRHeadRequest{RepoRoot: "/owner/repo", Remote: "origin", Number: 42})
	if err != nil || branch != "ocman/pr-42" {
		t.Fatalf("FetchPRHead() = %q, %v", branch, err)
	}
}
