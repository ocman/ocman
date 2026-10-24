package claudeprovider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// transcripts lists every transcript and registers directory watches.
// An unreadable directory is not evidence that its sessions were deleted.
func (x *Index) transcripts() ([]string, error) {
	x.watch(filepath.Dir(x.projects))
	x.watch(x.projects)
	projects, err := os.ReadDir(x.projects)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var paths []string
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(x.projects, p.Name())
		x.watch(dir)
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				// Pre-2.x sidechains have no parent link and are not sessions.
				if strings.HasSuffix(e.Name(), ".jsonl") && !strings.HasPrefix(e.Name(), "agent-") {
					paths = append(paths, filepath.Join(dir, e.Name()))
				}
				continue
			}
			x.watch(filepath.Join(dir, e.Name()))
			sub := filepath.Join(dir, e.Name(), "subagents")
			agents, err := x.subagentTranscripts(sub)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			paths = append(paths, agents...)
		}
	}
	return paths, nil
}

// The SDK also stores workflow agents below subagents/workflows/<runId>/.
// Do not follow symlink directories or hide an unreadable subtree as empty.
func (x *Index) subagentTranscripts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	x.watch(dir)
	var paths []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			children, err := x.subagentTranscripts(path)
			if err != nil {
				return nil, err
			}
			paths = append(paths, children...)
		} else if e.Type().IsRegular() && strings.HasPrefix(e.Name(), "agent-") && strings.HasSuffix(e.Name(), ".jsonl") {
			paths = append(paths, path)
		}
	}
	return paths, nil
}
