package claudeprovider

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// HistoryReader borrows the browsing supervisor's index. It never creates a
// second index writer or calls the live/session lifecycle handlers.
type HistoryReader struct {
	index *Index
	live  *live
}

func (h *HistoryReader) Inventory(ctx context.Context) ([]db.HistoryEntry, error) {
	x := h.index
	if err := lockHistory(ctx, x); err != nil {
		return nil, err
	}
	defer x.mu.Unlock()
	if _, err := os.Stat(x.projects); err != nil {
		return nil, err // a missing source root is not an authoritative empty inventory
	}
	// Only a scan that just succeeded makes the cached rows authoritative;
	// unchanged transcripts are only stat'ed.
	if err := x.sync(ctx); err != nil {
		return nil, err
	}
	// Newest first, so the sessions a user is most likely to resume import first.
	entries, err := x.query(ctx, "1=1 ORDER BY mtime DESC, id")
	if err != nil {
		return nil, err
	}
	out := make([]db.HistoryEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, db.HistoryEntry{ID: e.ID, Version: historyVersion(e)})
	}
	return out, nil
}

func historyVersion(e *entry) string { return fmt.Sprintf("%d:%d", e.Mtime, e.Size) }

func (h *HistoryReader) Snapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
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
	f, err := os.Open(e.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if before.ModTime().UnixNano() != e.Mtime || before.Size() != e.Size {
		return nil, db.ErrHistoryChanged
	}
	children, err := x.children(ctx, id)
	if err != nil {
		return nil, err
	}
	c := &Converter{SessionID: id, Children: children, Messages: []db.Message{}, Parts: []db.Part{}}
	reader := bufio.NewReaderSize(historyContextReader{ctx: ctx, reader: io.NewSectionReader(f, 0, e.End)}, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := c.AddChecked(raw); err != nil {
				return nil, fmt.Errorf("session %s: %w", id, err)
			}
		}
		if err == io.EOF {
			break
		}
	}
	c.Finish()
	after, err := os.Stat(e.Path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, db.ErrHistoryChanged
	}
	// Claude subagents are transcripts under a root session's subagents/ directory.
	return &db.HistorySnapshot{Session: e.session(time.Time{}), Messages: c.Messages, Parts: c.Parts, Version: historyVersion(e),
		Subagent: e.ID != e.RootID}, nil
}

func lockHistory(ctx context.Context, index *Index) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index.mu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type historyContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r historyContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
