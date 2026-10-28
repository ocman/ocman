package conversationingest

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/historyjournal"
)

func TestJournalConsumerUsesOwnerResolvedEntriesBeforeTranslation(t *testing.T) {
	c, store, _, dir, _ := journalFixture(t)
	appendRecord(t, dir, "hooks.jsonl", "native", "message")
	reads := 0
	c.read = func(_ context.Context, segment string, offset int64, limit int) ([]historyjournal.Entry, error) {
		reads++
		entries, err := historyjournal.ReadSegment(dir, segment, offset, limit)
		for i := range entries {
			entries[i].Record.Session = "ocm_canonical"
		}
		return entries, err
	}
	if count, err := c.drain(t.Context()); err != nil || count != 1 || reads < 1 {
		t.Fatalf("resolved journal drain: %d %d %v", count, reads, err)
	}
	if got := messageIDs(t, store, "ocm_canonical"); len(got) != 1 || got[0] != "message" {
		t.Fatalf("canonical journal messages: %+v", got)
	}
	if got := messageIDs(t, store, "native"); len(got) != 0 {
		t.Fatal("raw native journal record reached history storage")
	}
}

func TestJournalConsumerRetriesIdentityResolutionWithoutAdvancingOffset(t *testing.T) {
	c, store, _, dir, _ := journalFixture(t)
	appendRecord(t, dir, "hooks.jsonl", "native", "message")
	cause := errors.New("identity resolution pending")
	c.read = func(context.Context, string, int64, int) ([]historyjournal.Entry, error) { return nil, cause }
	if count, err := c.drain(t.Context()); !errors.Is(err, cause) || count != 0 {
		t.Fatalf("unresolved journal: %d %v", count, err)
	}
	if cursor, err := store.Checkpoint(t.Context(), c.instanceID, journalStream("hooks.jsonl")); err != nil || cursor != "" {
		t.Fatalf("unresolved hook was acknowledged: %s %v", cursor, err)
	}
}
