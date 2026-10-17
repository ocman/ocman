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
		{"/ramtmp/", "/ramtmp"},
		{"/private/tmp", "/private/tmp"},
		{"/var/folders/fw/6n6_9w296qx5qpg6brcjxfmr0000gn/T/", "/tmp"},
	} {
		if got := shortTempRoot(tc.tmp); got != tc.want {
			t.Errorf("shortTempRoot(%q) = %q, want %q", tc.tmp, got, tc.want)
		}
	}
}

func TestShortTempDirHonoursShortTMPDIR(t *testing.T) {
	root := t.TempDir()
	if len(root) > maxShortTempRoot {
		// Exercise the fallback instead: the root is too long to use.
		root = "/tmp"
	} else {
		t.Setenv("TMPDIR", root)
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
