package claudeprovider

import (
	"database/sql"
	"errors"
	"testing"
)

func TestHistoryRefreshEntryRemovedDuringReindex(t *testing.T) {
	x := openSynced(t, fixture(t))
	h := &HistoryReader{index: x}
	_, cursor, err := h.Delta(t.Context(), mainID, "")
	if err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, x, mainID, `{"type":"user","uuid":"new-user","message":{"role":"user","content":"changed"}}`)
	if _, err := x.db.Exec(`UPDATE session SET mtime=0 WHERE id=?`, mainID); err != nil {
		t.Fatal(err)
	}
	// Reproduce index.index's successful removal outcome deterministically at
	// the refresh boundary, without a timing-dependent filesystem race.
	if _, err := x.db.Exec(`CREATE TRIGGER remove_refreshed_session AFTER INSERT ON session BEGIN DELETE FROM session WHERE id=NEW.id; END`); err != nil {
		t.Fatal(err)
	}
	_, _, err = h.Delta(t.Context(), mainID, cursor)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed refresh entry: %v", err)
	}
}
