package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOneOffDirectories(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	h := &Host{}
	first, err := h.CreateOneOffDirectory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.CreateOneOffDirectory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !filepath.IsAbs(first) || filepath.Dir(first) != os.TempDir() {
		t.Fatalf("not distinct temporary directories: %q %q", first, second)
	}
	info, err := os.Stat(first)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("workspace is not private: %v, %v", info, err)
	}
	entries, err := os.ReadDir(first)
	if err != nil || len(entries) != 0 {
		t.Fatalf("workspace is not empty: %v, %v", entries, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.CreateOneOffDirectory(ctx); err == nil {
		t.Fatal("cancelled allocation succeeded")
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if _, err := h.CreateOneOffDirectory(t.Context()); err == nil {
		t.Fatal("allocation without a writable temp root succeeded")
	}
}
