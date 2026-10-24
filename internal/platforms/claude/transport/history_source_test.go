package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigIdentityIsStableBeforeFirstClaudeSession(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "config-parent")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(link, ".claude")
	before, err := canonicalHistoryConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := canonicalHistoryConfig(path)
	if err != nil || before != after {
		t.Fatalf("first initialization split physical identity: before=%q after=%q %v", before, after, err)
	}
	broken := filepath.Join(root, "broken")
	if err := os.Symlink(filepath.Join(root, "absent"), broken); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalHistoryConfig(broken); err == nil {
		t.Fatal("dangling config symlink accepted as an ordinary missing directory")
	}
}
