package term

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
)

func TestResourcePIDs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	path := filepath.Join(dir, "tmux")
	testutil.WriteScript(t, path, "#!/bin/sh\n[ \"$2\" = 'ocman-term' ] || exit 1\necho '100 101 bad -2 0 102'\n")
	if got := ResourcePIDs(context.Background()); !slices.Equal(got, []int{100, 101, 102}) {
		t.Fatal(got)
	}
	testutil.WriteScript(t, path, "#!/bin/sh\nexit 1\n")
	if got := ResourcePIDs(context.Background()); got != nil {
		t.Fatal(got)
	}
}
