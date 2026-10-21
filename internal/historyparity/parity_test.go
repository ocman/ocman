package historyparity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type fakeSource struct {
	key    string
	days   []db.DailyActivity
	models []db.ModelUsage
	err    error
}

func (f *fakeSource) HistorySourceKey(context.Context) (string, error) { return f.key, f.err }
func (f *fakeSource) GetDailyActivity(context.Context, int64, string, string) ([]db.DailyActivity, error) {
	return f.days, f.err
}
func (f *fakeSource) GetModelUsage(context.Context, int64, string) ([]db.ModelUsage, error) {
	return f.models, f.err
}

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)

func millis(day string) int64 {
	at, _ := time.ParseInLocation("2006-01-02", day, time.Local)
	return at.Add(9 * time.Hour).UnixMilli()
}

// history stores one root session with a user prompt and two assistant turns
// on 2026-10-01, and one assistant turn today, which comparisons skip.
func history(t *testing.T) *statev2.DB {
	t.Helper()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "oc", OwnerID: "owner", Platform: "opencode", SourceKey: "/opencode.db"}); err != nil {
		t.Fatal(err)
	}
	day := millis("2026-10-01")
	_, err = store.Commit(t.Context(), statev2.Batch{Complete: true,
		Session: statev2.Session{Key: statev2.Key{InstanceID: "oc", SessionID: "s"}, Title: "Root", CreatedAt: day, UpdatedAt: day},
		Messages: []statev2.Message{
			{ID: "u", Position: 0, Role: "user", CreatedAt: day},
			{ID: "a", Position: 1, Role: "assistant", Model: "anthropic/claude", CreatedAt: day, TokensInput: 10, TokensOutput: 4},
			{ID: "b", Position: 2, Role: "assistant", Model: "anthropic/claude", CreatedAt: day, TokensInput: 1, TokensCacheRead: 2},
			{ID: "t", Position: 3, Role: "assistant", Model: "anthropic/claude", CreatedAt: now.UnixMilli(), TokensInput: 5},
		},
		Checkpoint: &statev2.CheckpointUpdate{Stream: "s", Cursor: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func matchingSource() *fakeSource {
	return &fakeSource{key: "/opencode.db",
		days: []db.DailyActivity{
			{Date: "2026-09-30"},
			{Date: "2026-10-01", Sessions: 1, Messages: 2, UserMessages: 1},
			{Date: "2026-10-19", Messages: 9}, // today: still being written, not compared
		},
		models: []db.ModelUsage{{Provider: "anthropic", Model: "claude", Count: 3, TokensIn: 16, TokensOut: 4, CacheRead: 2}},
	}
}

func deps(source Source, store *statev2.DB) Deps {
	return Deps{Source: source, Store: store, OwnerID: "owner", Now: func() time.Time { return now }}
}

func TestMatchingSourcesReportNoMismatches(t *testing.T) {
	report, err := Compare(t.Context(), deps(matchingSource(), history(t)))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Mismatches) != 0 || report.Days != 2 || report.Models != 1 {
		t.Fatalf("report: %+v", report)
	}
}

func TestMismatchesNameTheKeyAndField(t *testing.T) {
	source := matchingSource()
	source.days[1].UserMessages = 3
	source.models[0].TokensOut = 99
	source.models = append(source.models, db.ModelUsage{Model: "only-in-source", Count: 1})
	report, err := Compare(t.Context(), deps(source, history(t)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[Mismatch]bool{
		{Kind: "day", Key: "2026-10-01", Field: "userMessages", Source: 3, History: 1}:       true,
		{Kind: "model", Key: "anthropic/claude", Field: "tokensOut", Source: 99, History: 4}: true,
		{Kind: "model", Key: "only-in-source", Field: "count", Source: 1, History: 0}:        true,
	}
	if len(report.Mismatches) != len(want) {
		t.Fatalf("mismatches: %+v", report.Mismatches)
	}
	for _, m := range report.Mismatches {
		if !want[m] {
			t.Fatalf("unexpected mismatch %+v in %+v", m, report.Mismatches)
		}
	}
}

func TestMissingInstanceAndSourceErrors(t *testing.T) {
	source := matchingSource()
	source.key = "/other.db"
	if _, err := Compare(t.Context(), deps(source, history(t))); !errors.Is(err, statev2.ErrNotFound) {
		t.Fatalf("unknown source: %v", err)
	}
	failing := matchingSource()
	failing.err = errors.New("analytics unavailable")
	if _, err := Compare(t.Context(), deps(failing, history(t))); err == nil {
		t.Fatal("source failure ignored")
	}
}
