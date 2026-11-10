package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
)

type inspectionRuntimeClone struct {
	*fakeRuntime
	clone ocruntime.Runtime
	err   error
	root  string
}

func (r *inspectionRuntimeClone) ForInspection(root string) (ocruntime.Runtime, error) {
	r.root = root
	return r.clone, r.err
}

func TestOpenCodeInspectionFailureBoundariesNeverBorrowRuntime(t *testing.T) {
	inspect := func(context.Context, string) (platforms.ConnectionInspection, error) {
		return platforms.ConnectionInspection{Ready: true}, nil
	}
	for _, mode := range []string{"relative-root", "nil-runtime", "blocked-root", "clone-failure", "launch-failure", "cleanup-failure", "private-clone"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			runtime := &fakeRuntime{}
			var supplied ocruntime.Runtime = runtime
			switch mode {
			case "relative-root":
				root = "relative"
			case "nil-runtime":
				supplied = nil
			case "blocked-root":
				root = filepath.Join(root, "file")
				if err := os.WriteFile(root, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "clone-failure":
				supplied = &inspectionRuntimeClone{fakeRuntime: runtime, err: errors.New("cannot isolate")}
			case "launch-failure":
				runtime.launchErr = errors.New("native failed to launch")
			case "cleanup-failure":
				runtime.stopErr = errors.New("native failed to stop")
			case "private-clone":
				supplied = &inspectionRuntimeClone{fakeRuntime: &fakeRuntime{}, clone: runtime}
			}
			result, err := InspectOpenCodeConnection(t.Context(), root, supplied, inspect)
			if mode == "private-clone" {
				cloner := supplied.(*inspectionRuntimeClone)
				if err != nil || !result.Ready || runtime.launchCount() != 1 || runtime.stopCount() != 1 || cloner.launchCount() != 0 || filepath.Dir(cloner.root) != root {
					t.Fatalf("clone ownership lost: %+v %v", result, err)
				}
				return
			}
			if err == nil || result.Ready {
				t.Fatalf("inspection failed open: %+v %v", result, err)
			}
			if mode == "cleanup-failure" && runtime.stopCount() != 1 {
				t.Fatal("cleanup was skipped")
			}
			if mode == "clone-failure" && runtime.launchCount() != 0 {
				t.Fatal("fell back to original runtime")
			}
		})
	}
}

func TestInspectionRuntimeStopRejectsUnownedAndIsIdempotent(t *testing.T) {
	runtime := &fakeRuntime{}
	owned := &inspectionOwnedRuntime{Runtime: runtime}
	if err := owned.stopOwned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := owned.Stop(t.Context(), &ocruntime.Instance{ID: "foreign"}); err == nil || runtime.stopCount() != 0 {
		t.Fatal("stopped unowned native runtime", err)
	}
	if _, err := owned.Launch(t.Context(), ocruntime.LaunchSpec{RepoRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := owned.stopOwned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := owned.stopOwned(t.Context()); err != nil || runtime.stopCount() != 1 {
		t.Fatal("owned runtime stopped twice", err)
	}
}

func TestInspectionInventoryNeverAliasesOrRetainsDeletedRows(t *testing.T) {
	store := &inspectionOpenCodeStore{rows: map[string]ManagedInstance{}}
	if err := store.Upsert(t.Context(), "/private", ManagedInstance{RuntimeID: "owned"}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	delete(rows, "/private")
	if _, ok, err := store.Get(t.Context(), "/private"); err != nil || !ok {
		t.Fatal("inventory exposed mutable map", err)
	}
	if err := store.Delete(t.Context(), "/private"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(t.Context(), "/private"); err != nil || ok {
		t.Fatal("deleted inspection handle retained", err)
	}
}
