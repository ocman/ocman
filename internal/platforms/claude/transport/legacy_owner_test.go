package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestLegacyOwnerWithoutProjectRootUsesRecordedDirectory(t *testing.T) {
	e := newRecoveryEnv(t)
	worktree := filepath.Join(e.data, "worktree")
	if out, err := gitexec.Command(t.Context(), "-C", e.repo, "worktree", "add", "-b", "legacy-worktree", worktree).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %s %v", out, err)
	}
	path := filepath.Join(t.TempDir(), "owned.json")
	raw, _ := json.Marshal(map[string]any{"legacy": map[string]any{"directory": worktree, "started": true}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	right := sseBrowser(t, "legacy")
	wrong := sseBrowser(t, "legacy")
	c := &claudeProjects{statePath: path, roots: map[string]*claudeProject{
		e.repo:       {watched: watched{adapter: right}},
		"/unrelated": {watched: watched{adapter: wrong}},
	}}
	if c.owner("legacy") != right {
		t.Fatal("legacy worktree ownership lost the attached supervisor")
	}
	delete(c.roots, e.repo)
	if c.owner("legacy") != nil {
		t.Fatal("legacy owner fell through to an unrelated transcript cache")
	}
}
