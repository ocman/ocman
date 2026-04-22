// Package gitinfo provides a cached, read-only view of the current
// git status for a working directory. It is used by the session list
// to surface the branch + a tiny change summary next to each session's
// project path.
//
// Implementation: shells out to `git status --porcelain=v2 --branch`
// once per directory, caches the parsed result in-memory with a short
// TTL, and never performs network operations. Ahead/behind counts
// therefore reflect whatever git already knows about the upstream from
// the most recent fetch — we don't trigger a fetch ourselves.
//
// The cache is keyed per absolute directory path. Negative results
// (not a git repo, or git unavailable) are cached too so repeated
// lookups don't keep spawning `git`.
package gitinfo

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info is the per-directory snapshot surfaced to callers. The zero
// value is safe to serialise and means "not a git repo / unknown".
type Info struct {
	// Branch is the short branch name (e.g. "main"). Empty when the
	// directory isn't a git worktree. For a detached HEAD we emit
	// "(detached)".
	Branch string `json:"branch"`
	// Ahead/Behind are relative to the upstream. Both are 0 when no
	// upstream is configured or when no commits have diverged.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// Dirty is true when the working tree or index has any changes,
	// including untracked files.
	Dirty bool `json:"dirty"`
}

// IsRepo is a convenience: a non-empty branch implies the lookup
// succeeded against a git worktree.
func (i Info) IsRepo() bool { return i.Branch != "" }

// Lookup returns the cached git info for dir, refreshing it when the
// cached entry is older than the default TTL. dir must be an absolute
// path to a directory.
//
// Lookup is safe for concurrent use. When dir is not a git worktree
// (or git itself is unavailable) it returns a zero Info and caches
// that result for the same TTL.
func Lookup(ctx context.Context, dir string) Info {
	return defaultCache.lookup(ctx, dir)
}

// --- cache ---

const defaultTTL = 30 * time.Second

type cacheEntry struct {
	info    Info
	fetched time.Time
}

type cache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	// fetch is injected so tests can stub out the git invocation.
	fetch func(ctx context.Context, dir string) Info
}

func newCache(ttl time.Duration, fetch func(ctx context.Context, dir string) Info) *cache {
	return &cache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		fetch:   fetch,
	}
}

var defaultCache = newCache(defaultTTL, fetchFromGit)

func (c *cache) lookup(ctx context.Context, dir string) Info {
	if dir == "" {
		return Info{}
	}

	c.mu.Lock()
	if e, ok := c.entries[dir]; ok && time.Since(e.fetched) < c.ttl {
		info := e.info
		c.mu.Unlock()
		return info
	}
	c.mu.Unlock()

	// Run the (potentially slow) fetch outside the lock so concurrent
	// lookups against different dirs don't serialise. A rare
	// duplicate fetch for the same dir is cheap — the alternative
	// (singleflight) adds complexity for little gain here.
	info := c.fetch(ctx, dir)

	c.mu.Lock()
	c.entries[dir] = &cacheEntry{info: info, fetched: time.Now()}
	c.mu.Unlock()

	return info
}

// --- git invocation ---

// gitCommandTimeout bounds each `git status` call. Most finish in a
// few ms, but a pathological repo (or a slow filesystem) shouldn't
// block the sessions endpoint indefinitely.
const gitCommandTimeout = 2 * time.Second

func fetchFromGit(ctx context.Context, dir string) Info {
	cctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "git", "-C", dir,
		"status", "--porcelain=v2", "--branch",
		"--untracked-files=normal", "--no-renames")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_NO_LOCK=1")

	out, err := cmd.Output()
	if err != nil {
		return Info{}
	}
	info := parsePorcelainV2(string(out))
	if info.Branch == "" {
		return Info{}
	}
	return info
}

// parsePorcelainV2 extracts the fields we care about from `git status
// --porcelain=v2 --branch` output. It is intentionally lenient: any
// line it doesn't recognise is ignored, so new porcelain additions
// won't break us.
func parsePorcelainV2(out string) Info {
	var info Info
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			info.Branch = strings.TrimPrefix(line, "# branch.head ")
			if info.Branch == "(detached)" {
				// Keep the parenthesised form — the UI can decide
				// how to render it. It's still a valid Branch for
				// IsRepo purposes.
			}
		case strings.HasPrefix(line, "# branch.ab "):
			// Format: "# branch.ab +<ahead> -<behind>"
			parts := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			for _, p := range parts {
				if len(p) < 2 {
					continue
				}
				n, err := strconv.Atoi(p[1:])
				if err != nil {
					continue
				}
				switch p[0] {
				case '+':
					info.Ahead = n
				case '-':
					info.Behind = n
				}
			}
		case strings.HasPrefix(line, "1 "),
			strings.HasPrefix(line, "2 "),
			strings.HasPrefix(line, "u "),
			strings.HasPrefix(line, "? "):
			info.Dirty = true
		}
	}
	return info
}
