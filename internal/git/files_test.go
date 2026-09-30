package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListAndReadFiles(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	outside := filepath.Join(t.TempDir(), "secret")
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".env\n")
	write(".env", "TOKEN=x")
	write("src/main.go", "package main")
	write("new.txt", "untracked")
	write("bin.dat", "a\x00b")
	if err := os.WriteFile(outside, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "src/main.go", ".gitignore", "link")
	ctx := context.Background()

	// Listing from a subdirectory is still rooted at the repo root.
	list, err := ListFiles(ctx, filepath.Join(dir, "src"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(list.Files)
	want := []string{".gitignore", "bin.dat", "foo.txt", "link", "new.txt", "src/main.go"}
	if !slices.Equal(list.Files, want) {
		t.Fatalf("files = %v, want %v", list.Files, want)
	}

	got, err := ReadFile(ctx, dir, "src/main.go")
	if err != nil || got.Content != "package main" || got.Size != 12 {
		t.Fatalf("ReadFile = %+v, %v", got, err)
	}
	if got, err := ReadFile(ctx, dir, "bin.dat"); err != nil || !got.Binary || got.Content != "" {
		t.Fatalf("binary = %+v, %v", got, err)
	}
	for _, p := range []string{".env", "../secret", "link", "src", "missing.go", "src/*.go", outside} {
		if _, err := ReadFile(ctx, dir, p); !errors.Is(err, ErrFileNotFound) {
			t.Errorf("ReadFile(%q) err = %v, want ErrFileNotFound", p, err)
		}
	}
	if _, err := ListFiles(ctx, t.TempDir()); !errors.Is(err, ErrNotARepo) {
		t.Errorf("non-repo err = %v", err)
	}
}
