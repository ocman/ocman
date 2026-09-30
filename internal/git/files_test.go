package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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

// Review regressions: a listed name must not reach a hidden target, and
// special files or git failures must not masquerade as missing files.
func TestReadFileRefusesHiddenTargets(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=x"), 0o644))
	must(os.Symlink(".env", filepath.Join(dir, "public.txt")))
	must(os.Symlink(".git/config", filepath.Join(dir, "cfg")))
	must(os.MkdirAll(filepath.Join(dir, "docs"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "docs", "HEAD"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "pipe"), []byte("x"), 0o644))
	gitRun(t, dir, "add", ".gitignore", "public.txt", "cfg", "docs/HEAD", "pipe")
	// A tracked directory swapped for a link into .git, and a tracked
	// file swapped for a FIFO: both stay in the index.
	must(os.RemoveAll(filepath.Join(dir, "docs")))
	must(os.Symlink(".git", filepath.Join(dir, "docs")))
	must(os.Remove(filepath.Join(dir, "pipe")))
	must(unix.Mkfifo(filepath.Join(dir, "pipe"), 0o644))

	for _, p := range []string{"public.txt", "cfg", "docs/HEAD", "pipe"} {
		done := make(chan error, 1)
		go func() { _, err := ReadFile(ctx, dir, p); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrFileNotFound) {
				t.Errorf("ReadFile(%q) err = %v, want ErrFileNotFound", p, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("ReadFile(%q) blocked", p)
		}
	}

	// A git failure is an error, not a 404.
	must(os.WriteFile(filepath.Join(dir, ".git", "index"), []byte("garbage"), 0o644))
	if _, err := ReadFile(ctx, dir, ".gitignore"); err == nil || errors.Is(err, ErrFileNotFound) {
		t.Errorf("corrupt index err = %v, want an operational error", err)
	}
}

func TestListFilesStopsAtBudget(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defer func(n, b int) { MaxListedFiles, MaxListedBytes = n, b }(MaxListedFiles, MaxListedBytes)
	ctx := context.Background()

	MaxListedFiles = 3
	list, err := ListFiles(ctx, dir)
	if err != nil || len(list.Files) != 3 || !list.Truncated {
		t.Fatalf("count cap: %+v, %v", list, err)
	}
	MaxListedFiles, MaxListedBytes = 100, 5 // foo.txt (7 bytes) alone overflows
	list, err = ListFiles(ctx, dir)
	if err != nil || !list.Truncated || len(list.Files) > 2 {
		t.Fatalf("byte cap: %+v, %v", list, err)
	}
	MaxListedBytes = 1 << 20
	if list, err = ListFiles(ctx, dir); err != nil || list.Truncated || len(list.Files) != 6 {
		t.Fatalf("under cap: %+v, %v", list, err)
	}
}
