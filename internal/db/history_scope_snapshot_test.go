package db

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

// A scoped reader keeps reading the revision it validated, even after a
// colliding source commits; the next scope then fails closed.
func TestScopedHistoryReaderReadsTheValidatedRevision(t *testing.T) {
	path, store := historyPerformanceStore(t)
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "two", OwnerID: "owner", Platform: "p", SourceKey: "other"}); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenHistoryAnalytics(path, []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	scoped, release, err := reader.ScopedHistoryReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := store.Commit(t.Context(), statev2.Batch{
		Session:  statev2.Session{Key: statev2.Key{InstanceID: "two", SessionID: "first"}},
		Messages: []statev2.Message{{ID: "other", Role: "assistant", CreatedAt: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := scoped.GetStats(t.Context())
	if err != nil || stats.TotalSessions != 1 {
		t.Fatalf("snapshot saw a later commit: %+v %v", stats, err)
	}
	if _, _, err := reader.ScopedHistoryReader(t.Context()); err == nil {
		t.Fatal("collision committed after the snapshot was not rejected")
	}
	if _, _, err := openTestDB(t).ScopedHistoryReader(t.Context()); err == nil {
		t.Fatal("accepted a non-history reader")
	}
}
