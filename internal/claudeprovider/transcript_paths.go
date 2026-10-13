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
			agents, err := os.ReadDir(sub)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			x.watch(sub)
			for _, a := range agents {
				if !a.IsDir() && strings.HasPrefix(a.Name(), "agent-") && strings.HasSuffix(a.Name(), ".jsonl") {
					paths = append(paths, filepath.Join(sub, a.Name()))
				}
			}
		}
	}
	return paths, nil
}
