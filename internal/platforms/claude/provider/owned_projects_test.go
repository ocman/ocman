package claudeprovider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestOwnershipRetainsProjectIdentityThroughCreateForkAndLegacyBackfill(t *testing.T) {
	dir := t.TempDir()
	idx, err := OpenIndex(filepath.Join(dir, "index.db"), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	l := &live{idx: idx, opts: Options{StatePath: filepath.Join(dir, "owned.json"), ProjectRoot: "/repo"}, owned: map[string]*owned{}}
	created, err := l.create(platforms.CreateSessionRequest{Directory: "/arbitrary/linked/checkout"})
	if err != nil {
		t.Fatal(err)
	}
	check := func(id string) {
		t.Helper()
		roots, err := OwnedProjectRoots(l.opts.StatePath)
		if err != nil || roots[id] != "/repo" {
			t.Fatal(roots, err)
		}
		if _, exists := roots["terminal"]; exists {
			t.Fatal("unmanaged session acquired ownership")
		}
	}
	check(created.ID)
	l.mu.Lock()
	err = l.updateOwnedLocked(func() { l.owned[created.ID].ProjectRoot = ""; l.owned[created.ID].Started = true })
	if err == nil {
		err = l.markStartedLocked(created.ID)
	}
	l.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	check(created.ID)
	forked, err := l.fork(t.Context(), created.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	check(forked.ID)
	// A browsing fork without a project option retains its source's identity.
	l.opts.ProjectRoot = ""
	forked, err = l.fork(t.Context(), created.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	check(forked.ID)
}

func TestHistoricalOwnershipErrorsAndLegacyUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if _, err := OwnedProjectRoots(path); err == nil {
		t.Fatal("missing ownership accepted")
	}
	for _, data := range []string{"invalid", "null", `{"s":{"projectRoot":"relative"}}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := OwnedProjectRoots(path); err == nil {
			t.Fatal("invalid ownership accepted", data)
		}
	}
	if err := os.WriteFile(path, []byte(`{"legacy":{"directory":"/gone"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	roots, err := OwnedProjectRoots(path)
	if root, exists := roots["legacy"]; err != nil || !exists || root != "" {
		t.Fatal("legacy unknown treated as unmanaged", roots, err)
	}
}
