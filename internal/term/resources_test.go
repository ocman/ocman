package term

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestResourcePIDs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$2\" = 'ocman-term' ] || exit 1\necho '100 101 bad -2 0 102'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := ResourcePIDs(context.Background()); !slices.Equal(got, []int{100, 101, 102}) {
		t.Fatal(got)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := ResourcePIDs(context.Background()); got != nil {
		t.Fatal(got)
	}
}
