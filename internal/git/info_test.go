package git

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePorcelainV2_CleanOnBranch(t *testing.T) {
	out := `# branch.oid abc123
# branch.head main
# branch.upstream origin/main
# branch.ab +0 -0
`
	info := parsePorcelainV2(out)
	if info.Branch != "main" {
		t.Errorf("branch = %q, want main", info.Branch)
	}
	if info.Ahead != 0 || info.Behind != 0 {
		t.Errorf("ahead/behind = %d/%d, want 0/0", info.Ahead, info.Behind)
	}
	if info.Dirty {
		t.Error("expected clean, got dirty")
	}
	if !info.IsRepo() {
		t.Error("expected IsRepo true")
	}
}

func TestParsePorcelainV2_DirtyAndAheadBehind(t *testing.T) {
	out := `# branch.oid abc
# branch.head feature/x
# branch.upstream origin/feature/x
# branch.ab +3 -1
1 .M N... 100644 100644 100644 a a foo.go
? untracked.txt
`
	info := parsePorcelainV2(out)
	if info.Branch != "feature/x" {
		t.Errorf("branch = %q", info.Branch)
	}
	if info.Ahead != 3 {
		t.Errorf("ahead = %d, want 3", info.Ahead)
	}
	if info.Behind != 1 {
		t.Errorf("behind = %d, want 1", info.Behind)
	}
	if !info.Dirty {
		t.Error("expected dirty")
	}
}

func TestParsePorcelainV2_Detached(t *testing.T) {
	out := `# branch.oid abc
# branch.head (detached)
`
	info := parsePorcelainV2(out)
	if info.Branch != "(detached)" {
		t.Errorf("branch = %q, want (detached)", info.Branch)
	}
	if info.Dirty {
		t.Error("expected not dirty")
	}
}

func TestParsePorcelainV2_NoUpstream(t *testing.T) {
	// No branch.ab header when there's no upstream tracking.
	out := `# branch.oid abc
# branch.head topic
1 .M N... 100644 100644 100644 a a foo.go
`
	info := parsePorcelainV2(out)
	if info.Branch != "topic" {
		t.Errorf("branch = %q", info.Branch)
	}
	if info.Ahead != 0 || info.Behind != 0 {
		t.Errorf("ahead/behind = %d/%d, want 0/0", info.Ahead, info.Behind)
	}
	if !info.Dirty {
		t.Error("expected dirty")
	}
}

func TestParsePorcelainV2_UnmergedAndRenamed(t *testing.T) {
	out := `# branch.head main
2 R. N... 100644 100644 100644 a a R100 new.go` + "\t" + `old.go
u UU N... 100644 100644 100644 100644 a b c d conflict.go
`
	info := parsePorcelainV2(out)
	if !info.Dirty {
		t.Error("expected dirty (renamed + unmerged)")
	}
}

func TestParsePorcelainV2_Empty(t *testing.T) {
	info := parsePorcelainV2("")
	if info.IsRepo() {
		t.Error("empty output should not be a repo")
	}
}

func TestCache_HonoursTTLAndDedupes(t *testing.T) {
	var calls int64
	c := newCache(50*time.Millisecond, func(_ context.Context, dir string) Info {
		atomic.AddInt64(&calls, 1)
		return Info{Branch: "main"}
	})

	for i := 0; i < 5; i++ {
		got := c.lookup(context.Background(), "/tmp/x")
		if got.Branch != "main" {
			t.Fatalf("unexpected branch: %q", got.Branch)
		}
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("expected 1 fetch within TTL, got %d", got)
	}

	// Different dir = separate cache entry.
	c.lookup(context.Background(), "/tmp/y")
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("expected 2 fetches for two dirs, got %d", got)
	}

	// After TTL expiry, the same dir fetches again.
	time.Sleep(60 * time.Millisecond)
	c.lookup(context.Background(), "/tmp/x")
	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Fatalf("expected 3 fetches after TTL expiry, got %d", got)
	}
}

func TestCache_EmptyDirShortCircuits(t *testing.T) {
	var calls int64
	c := newCache(time.Minute, func(_ context.Context, _ string) Info {
		atomic.AddInt64(&calls, 1)
		return Info{Branch: "main"}
	})
	got := c.lookup(context.Background(), "")
	if got.IsRepo() {
		t.Error("empty dir should return zero Info")
	}
	if atomic.LoadInt64(&calls) != 0 {
		t.Error("empty dir should not invoke fetch")
	}
}

func TestLookup_NonGitDir(t *testing.T) {
	// /tmp is almost certainly not a git repo; either way, this
	// exercises the real git invocation path and confirms we don't
	// panic and return zero info on a non-repo.
	info := Lookup(context.Background(), t.TempDir())
	if info.IsRepo() {
		t.Errorf("temp dir unexpectedly reported as repo: %+v", info)
	}
}
