package claudeprovider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
)

func TestWorktreeName(t *testing.T) {
	bin := claudetest.Binary(t)
	dir := t.TempDir()
	record := filepath.Join(dir, "call.json")
	t.Setenv("FAKE_CLAUDE_PRINT_LOG", record)
	t.Setenv("FAKE_CLAUDE_PRINT", "\nFix Login Redirect!\n")
	name, err := WorktreeName(t.Context(), "", bin, dir, "the login redirect loops")
	if err != nil || name != "fix-login-redirect" {
		t.Fatalf("name = %q, %v", name, err)
	}
	var call struct {
		Args   []string
		Prompt string
	}
	data, _ := os.ReadFile(record)
	if err := json.Unmarshal(data, &call); err != nil || !slices.Equal(call.Args, worktreeNameArgs) || !strings.Contains(call.Prompt, "the login redirect loops") {
		t.Fatalf("call = %+v, %v", call, err)
	}

	t.Setenv("FAKE_CLAUDE_PRINT", "!!!")
	if _, err := WorktreeName(t.Context(), "", bin, dir, "x"); err == nil {
		t.Fatal("unsluggable answer accepted")
	}
	if _, err := WorktreeName(t.Context(), "", filepath.Join(dir, "missing"), dir, "x"); err == nil {
		t.Fatal("missing CLI accepted")
	}
}
