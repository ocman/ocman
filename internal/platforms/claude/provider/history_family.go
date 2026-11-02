package claudeprovider

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"

	"github.com/ocman/ocman/internal/db"
)

// Family discovers new children beneath one root, never another project's files.
func (h *HistoryReader) Family(ctx context.Context, id string) ([]db.HistoryEntry, error) {
	x := h.index
	if err := lockHistory(ctx, x); err != nil {
		return nil, err
	}
	defer x.mu.Unlock()
	e, err := x.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, sql.ErrNoRows
	}
	root, err := x.get(ctx, e.RootID)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, sql.ErrNoRows
	}
	paths := []string{root.Path}
	children, err := x.subagentTranscripts(strings.TrimSuffix(root.Path, ".jsonl") + "/subagents")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	paths = append(paths, children...)
	known, err := x.query(ctx, "root_id = ?", root.ID)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, path := range paths {
		present[path] = true
	}
	for _, e := range known {
		if !present[e.Path] {
			paths = append(paths, e.Path)
		}
	}
	if err := x.syncPaths(ctx, paths); err != nil {
		return nil, err
	}
	entries, err := x.query(ctx, "root_id = ? ORDER BY mtime DESC, id", root.ID)
	if err != nil {
		return nil, err
	}
	out := make([]db.HistoryEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, db.HistoryEntry{ID: e.ID, Version: historyVersion(e)})
	}
	return out, nil
}

// syncPaths refreshes only named files, including their root's parent links.
func (x *Index) syncPaths(ctx context.Context, paths []string) error {
	roots := map[string]bool{}
	for _, path := range paths {
		id, root := identify(path, x.projects)
		e, err := x.get(ctx, id)
		if err != nil {
			return err
		}
		st, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			if err := x.remove(ctx, path); err != nil {
				return err
			}
			roots[root] = true
			continue
		}
		if err != nil {
			return err
		}
		if e != nil && e.Path == path && e.Mtime == st.ModTime().UnixNano() && e.Size == st.Size() {
			continue
		}
		if err := x.index(ctx, path); err != nil {
			return err
		}
		roots[root] = true
	}
	for root := range roots {
		if err := x.link(ctx, root); err != nil {
			return err
		}
	}
	return nil
}
