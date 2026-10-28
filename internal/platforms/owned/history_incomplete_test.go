package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type partialHistory struct {
	*historyPlatform
	err error
}

func (p *partialHistory) HistoryInventory(context.Context) ([]db.HistoryEntry, error) {
	return p.entries, p.err
}

func TestCanonicalHistoryRetainsPartialInventoryAndFailureCause(t *testing.T) {
	a, p := historyFixture(t)
	cause := errors.New("attached supervisor unreachable")
	a.native = &partialHistory{historyPlatform: p, err: &platforms.HistoryDeferred{Count: 1, Err: cause}}
	rows, err := a.HistoryInventory(t.Context())
	var incomplete *platforms.HistoryDeferred
	if len(rows) != 1 || !sessioncatalog.IsID(rows[0].ID) || !errors.As(err, &incomplete) || incomplete.Count != 1 || !errors.Is(err, cause) {
		t.Fatalf("canonical adapter lost partial inventory: %+v %v", rows, err)
	}
}
