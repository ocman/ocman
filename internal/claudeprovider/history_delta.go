package claudeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"

	"github.com/NoUseFreak/ocman/internal/db"
)

// Replay the last message group, not merely bytes after EOF: an appended tool
// result or assistant chunk can update the last previously published message.
type transcriptCursor struct {
	Path                               string
	Offset, Position, End, Mtime, Size int64
	Prefix, Boundary, Children         string
}

func (h *HistoryReader) Delta(ctx context.Context, id, cursor string) (*db.HistorySnapshot, string, error) {
	var resume transcriptCursor
	_ = json.Unmarshal([]byte(cursor), &resume)
	snapshot, err := h.snapshot(ctx, id, &resume)
	if err != nil {
		return nil, "", err
	}
	return snapshot, snapshot.SourceCursor, nil
}

func (c transcriptCursor) valid(f *os.File, st os.FileInfo, e *entry, children map[string]string) bool {
	if c.Path != e.Path || c.Prefix == "" || c.Boundary == "" || c.Offset < 0 || c.Position < 0 || c.End <= 0 || c.End > e.End || c.Offset > c.End || c.Size > st.Size() {
		return false
	}
	// A same-sized rewrite is not an append. Prefix and boundary fingerprints
	// also detect replacement/truncation followed by growth before the next read.
	if c.Size == st.Size() && c.Mtime != st.ModTime().UnixNano() {
		return false
	}
	return c.Prefix == transcriptHash(f, 0, min(c.End, 4096)) &&
		c.Boundary == transcriptHash(f, max(0, c.End-4096), min(c.End, 4096)) &&
		c.Children == childrenHash(children)
}

func (h *HistoryReader) nextCursor(ctx context.Context, f *os.File, st os.FileInfo, e *entry, children map[string]string) (string, error) {
	c := transcriptCursor{Path: e.Path, End: e.End, Mtime: st.ModTime().UnixNano(), Size: st.Size(), Children: childrenHash(children),
		Prefix: transcriptHash(f, 0, min(e.End, 4096)), Boundary: transcriptHash(f, max(0, e.End-4096), min(e.End, 4096))}
	if e.Messages > 0 {
		if err := h.index.db.QueryRowContext(ctx, `SELECT offset,idx FROM message WHERE session_id=? ORDER BY idx DESC LIMIT 1`, e.ID).
			Scan(&c.Offset, &c.Position); err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(c)
	return string(raw), err
}

func transcriptHash(f *os.File, offset, size int64) string {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, offset, size)); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func childrenHash(children map[string]string) string {
	raw, _ := json.Marshal(children)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
