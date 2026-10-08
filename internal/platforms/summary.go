package platforms

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/db"
)

// SummaryReader reads a session list row without loading messages or trees.
type SummaryReader interface {
	SessionSummary(context.Context, string) (*db.Session, error)
}

// ReadSessionSummary keeps adapters and older remotes without summary support usable.
func ReadSessionSummary(ctx context.Context, p Platform, id string) (*db.Session, error) {
	if reader, ok := p.(SummaryReader); ok {
		row, err := reader.SessionSummary(ctx, id)
		if !errors.Is(err, ErrUnsupported) {
			return row, err
		}
	}
	detail, err := p.Session(ctx, id, 1, 0)
	if err != nil || detail == nil {
		return nil, err
	}
	return detail.Session, nil
}
