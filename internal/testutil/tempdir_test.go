package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShortTempRoot(t *testing.T) {
	for _, tc := range []struct{ tmp, want string }{
		{"/tmp", "/tmp"},
		{"/tmp/ram/", "/tmp/ram"},
		{"/private/tmp", "/private/tmp"},
		{"/var/folders/fw/6n6_9w296qx5qpg6brcjxfmr0000gn/T/", "/tmp"},
	} {
		if got := shortTempRoot(tc.tmp); got != tc.want {
			t.Errorf("shortTempRoot(%q) = %q, want %q", tc.tmp, got, tc.want)
		}
	}
}

func TestShortTempDirHonoursShortTMPDIR(t *testing.T) {
	// Check the inherited root, including CI's short RAM-backed TMPDIR.
	// A nested t.TempDir is long even when its parent root is short.
	root := filepath.Clean(os.TempDir())
	if len(root) > maxShortTempRoot {
		// Long roots, such as macOS's per-user TMPDIR, fall back to /tmp.
		root = "/tmp"
	}
	var dir string
	t.Run("create", func(t *testing.T) {
		dir = ShortTempDir(t, "st-")
		if filepath.Dir(dir) != root || !strings.HasPrefix(filepath.Base(dir), "st-") {
			t.Fatalf("dir = %q, want st-* under %q", dir, root)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("stat %q: %v", dir, err)
		}
	})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir survived its test: %v", err)
	}
}
