package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// jsonlFile describes one top-level session jsonl on disk.
type jsonlFile struct {
	path  string // absolute path
	mtime int64  // ModTime as Unix milliseconds (for cache key comparison)
	size  int64
	// encodedDir is the directory name under projects/ — e.g.
	// `-Users-dries-src-github-com-NoUseFreak-ocman`. Kept only for
	// logging / debugging; authoritative cwd comes from the file's
	// first event (AD-11).
	encodedDir string
}

// scanSessionFiles enumerates every top-level <uuid>.jsonl under the
// Claude Code projects directory and returns a slice of descriptors.
//
// Skips:
//   - the `<uuid>/subagents/` tree; sub-agent transcripts are nested
//     one level below a session's directory and are handled by the
//     parent session's detail view, not listed independently.
//   - non-jsonl files.
//
// Directories that can't be read are skipped with a silent continue —
// the goal is "best available view of the user's history", and one
// unreadable project shouldn't blind the whole list.
func scanSessionFiles(projectsDir string) ([]jsonlFile, error) {
	projectEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}

	var out []jsonlFile
	for _, pe := range projectEntries {
		if !pe.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, pe.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				// Skip `<uuid>/subagents/` and any other nested
				// directories. Sub-agent transcripts are accessed
				// through their parent session, not listed.
				continue
			}
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			out = append(out, jsonlFile{
				path:       filepath.Join(dir, f.Name()),
				mtime:      info.ModTime().UnixMilli(),
				size:       info.Size(),
				encodedDir: pe.Name(),
			})
		}
	}
	return out, nil
}

// parseAllHeads runs a bounded-parallelism head-parse over a slice of
// jsonl files. Returns a slice in an unspecified order — callers that
// care about ordering should sort by TimeUpdated themselves.
//
// Per-file parse errors are silently dropped: one corrupt file must
// not blind the whole dashboard.
func parseAllHeads(files []jsonlFile, c *cache) []*parsedFile {
	if len(files) == 0 {
		return nil
	}

	// Bounded worker pool: reading + parsing ~1000 small files
	// concurrently would exhaust file descriptors on macOS (default
	// ulimit 256). 16 workers keeps us well under that and stays
	// disk-friendly.
	const workers = 16
	jobs := make(chan jsonlFile, len(files))
	results := make(chan *parsedFile, len(files))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results <- loadHead(job, c)
			}
		}()
	}

	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make([]*parsedFile, 0, len(files))
	for r := range results {
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}

// loadHead returns a cached head-parse for the given file, or
// parses + caches it if missing / stale. The cache stores the full
// parsed result; head and full modes read the same cached value
// (head mode just doesn't populate Messages/Parts, which is fine —
// a cache hit with Messages nil still satisfies a head request).
func loadHead(f jsonlFile, c *cache) *parsedFile {
	if c != nil {
		if pf := c.getByMtime(f.path, f.mtime, f.size); pf != nil {
			return pf
		}
	}
	pf, err := parseFile(f.path, parseHead)
	if err != nil {
		return nil
	}
	if c != nil {
		c.putByMtime(f.path, f.mtime, f.size, pf)
	}
	return pf
}

// loadFull returns a full parse of the given file — either the cached
// value (only if it was already a full parse, indicated by Messages
// being non-empty or UserMessageCount > 0 with a deliberately
// populated Messages slice), or a fresh read.
//
// Phase 4 goes a little conservative: we always re-parse for the
// detail view, since upgrading a head cache entry to a full one
// without re-reading would require tracking "was this a head or a
// full parse" on the cache entry. Detail reads are triggered by user
// click, not by periodic polling, so the extra cost is bounded.
func loadFull(f jsonlFile, c *cache) (*parsedFile, error) {
	pf, err := parseFile(f.path, parseFull)
	if err != nil {
		return nil, err
	}
	if c != nil {
		c.putByMtime(f.path, f.mtime, f.size, pf)
	}
	return pf, nil
}
