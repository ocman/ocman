package claudeprovider

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/ocman/ocman/internal/db"
)

// Head reads indexed scalar metadata and checks the physical file version.
// Unlike browsing entry refresh, this must never parse transcript content.
func (h *HistoryReader) Head(ctx context.Context, id string) (*db.Session, string, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", "", err
	}
	e, err := h.index.get(ctx, id)
	if err != nil {
		return nil, "", "", err
	}
	if e == nil {
		if h.live != nil {
			for _, row := range h.live.pending(nil, "") {
				if row.ID == id {
					return &row, "", "", nil
				}
			}
		}
		return nil, "", "", sql.ErrNoRows
	}
	info, err := os.Stat(e.Path)
	if os.IsNotExist(err) {
		return nil, "", "", sql.ErrNoRows
	}
	if err != nil {
		return nil, "", "", err
	}
	version := fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	if version != historyVersion(e) {
		return nil, "", "", db.ErrHistoryChanged
	}
	row := e.session(time.Time{})
	if h.live != nil {
		h.live.overlay(&row)
	}
	return &row, version, e.Model, nil
}
