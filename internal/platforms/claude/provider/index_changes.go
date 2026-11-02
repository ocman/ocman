package claudeprovider

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (x *Index) syncChanges(ctx context.Context, changes []string) error {
	if err := lockHistory(ctx, x); err != nil {
		return err
	}
	defer x.mu.Unlock()
	paths := map[string]bool{}
	for _, change := range changes {
		if change == "" {
			return x.sync(ctx)
		} // overflow or unknown watch
		change = filepath.Clean(change)
		rel, err := filepath.Rel(x.projects, change)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if strings.HasSuffix(change, ".meta.json") {
			path := strings.TrimSuffix(change, ".meta.json") + ".jsonl"
			if err := x.index(ctx, path); err != nil {
				return err
			}
			_, root := identify(path, x.projects)
			if err := x.link(ctx, root); err != nil {
				return err
			}
			continue
		}
		info, statErr := os.Stat(change)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if isTranscript(change, x.projects) && (info == nil || !info.IsDir()) {
			paths[change] = true
			continue
		}
		err = filepath.WalkDir(change, func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() {
				x.watch(path)
			} else if isTranscript(path, x.projects) {
				paths[path] = true
			}
			return ctx.Err()
		})
		if err != nil {
			return err
		}
		prefix := change + string(filepath.Separator)
		rows, err := x.db.QueryContext(ctx, `SELECT path FROM session WHERE path >= ? AND path < ?`, prefix, prefix+"\uffff")
		if err != nil {
			return err
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				rows.Close()
				return err
			}
			paths[path] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	list := make([]string, 0, len(paths))
	for path := range paths {
		list = append(list, path)
	}
	return x.syncPaths(ctx, list)
}

func isTranscript(path, projects string) bool {
	if !strings.HasSuffix(path, ".jsonl") {
		return false
	}
	id, root := identify(path, projects)
	return !strings.HasPrefix(filepath.Base(path), "agent-") || id != root
}
