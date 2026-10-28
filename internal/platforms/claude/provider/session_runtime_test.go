package claudeprovider

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestRebindSessionRuntimePreservesSharedSessionMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	original := map[string]*owned{
		"session": {Directory: "/moved/project", ProjectRoot: "/old", Title: "retained", Created: 123, Started: true, Config: "/config", ForkOf: "parent", TitlePending: true},
		"sibling": {Directory: "/sibling", ProjectRoot: "/sibling", Config: "/another-config"},
	}
	raw, _ := json.Marshal(original)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := RebindSessionRuntime(path, "session", "/actual-supervisor"); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]*owned
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	original["session"].ProjectRoot = "/actual-supervisor"
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("runtime binding overwrote shared metadata: %+v", got)
	}
	if err := RebindSessionRuntime(path, "unknown", "/actual-supervisor"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing native session acquired ownership: %v", err)
	}
	if err := RebindSessionRuntime(path, "session", "relative"); err == nil {
		t.Fatal("relative runtime root accepted")
	}
	if err := RebindSessionRuntime(path, "", "/root"); err == nil {
		t.Fatal("empty native identity accepted")
	}
}

func TestRebindSessionRuntimeFailsClosedOnPersistenceFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if err := os.WriteFile(path, []byte(`{"session":{"directory":"/worktree","projectRoot":"/old"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	if err := RebindSessionRuntime(path, "session", "/new"); err == nil {
		t.Fatal("runtime repair ignored a failed ownership lock")
	}
	if root, _ := OwnedProjectRoot(path, "session"); root != "/old" {
		t.Fatal("failed persistence changed the routing record")
	}
}
