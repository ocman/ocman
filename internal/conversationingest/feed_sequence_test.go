package conversationingest

import (
	"context"
	"testing"
)

// sequenceFeed also reads one session's sequence directly.
type sequenceFeed struct {
	*fakeFeed
	lookups []string
}

func (f *sequenceFeed) HistorySequence(_ context.Context, id string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, id)
	seq, ok := f.sequences[id]
	return seq, ok, nil
}

// A single-session repair must not list and resolve every session in the
// source: with thousands of sessions that turned each live refresh into
// thousands of catalog queries and starved every other reader.
func TestFeedRepairReadsOnlyTheRepairedSessionSequence(t *testing.T) {
	f := &sequenceFeed{fakeFeed: newFakeFeed()}
	w, store := feedImporter(t, f.fakeFeed)
	w.source.Reader, w.feed = f, f
	w.drainFeed(t.Context())
	listings := f.listings
	f.snapshots["session"] = historySnapshot("repaired")
	f.sequences["session"] = 11
	w.module.Notify("feed", "session")
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "repaired" {
		t.Fatalf("repair = %q", text)
	}
	if got := receipt(t, store, w); got != "seq:11" {
		t.Fatalf("repair receipt = %q", got)
	}
	if f.listings != listings {
		t.Fatalf("single-session repair listed all sequences %d times", f.listings-listings)
	}
	if len(f.lookups) == 0 || f.lookups[len(f.lookups)-1] != "session" {
		t.Fatalf("repair did not read the session sequence: %v", f.lookups)
	}
}
