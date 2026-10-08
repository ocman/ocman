package opencode

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionSummaryDoesNotCallLiveAPI(t *testing.T) {
	const id, dir = "old", "/tmp/summary"
	fake := newOpencodeFake(t)
	withTestPort(t, dir, fake.Port())
	a := New(newTestDBWithSession(t, id, dir), nil)
	row, err := a.SessionSummary(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != id || row.Platform != "opencode" || !row.LiveConnection {
		t.Fatalf("row = %+v", row)
	}
	if len(fake.hits) != 0 {
		t.Fatalf("live API called: %v", fake.hits)
	}
	if _, err := a.SessionSummary(t.Context(), "deleted"); err == nil {
		t.Fatal("deleted row returned")
	}
	if _, err := New(nil, nil).SessionSummary(t.Context(), id); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("nil DB: %v", err)
	}
}
