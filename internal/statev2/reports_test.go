package statev2

import (
	"errors"
	"testing"
	"time"
)

// reportSession commits one session with the given messages under instance
// "first". subagent is the adapter's decision; the title never decides it.
func reportSession(t *testing.T, d *DB, id, title string, subagent bool, created int64, deleted bool, messages ...Message) {
	t.Helper()
	b := Batch{Complete: true, Session: Session{Key: Key{InstanceID: "first", SessionID: id}, Title: title, Directory: "/repo",
		CreatedAt: created, UpdatedAt: created, Subagent: subagent}, Messages: messages,
		Checkpoint: &CheckpointUpdate{Stream: "s/" + id, Cursor: "1"}}
	commit(t, d, b)
	if deleted {
		b.Session.SourceDeleted, b.ExpectedRevision = true, 1
		b.Checkpoint = &CheckpointUpdate{Stream: "s/" + id, ExpectedCursor: "1", Cursor: "2"}
		commit(t, d, b)
	}
}

func dayMillis(t *testing.T, day string, hour int) int64 {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return at.Add(time.Duration(hour) * time.Hour).UnixMilli()
}

func TestReportsMatchAnalyticsRules(t *testing.T) {
	d := openTestDB(t)
	day1, day2 := dayMillis(t, "2026-10-01", 9), dayMillis(t, "2026-10-02", 9)
	reportSession(t, d, "root", "Root", false, day1, false,
		Message{ID: "u1", Position: 0, Role: "user", CreatedAt: day1},
		Message{ID: "a1", Position: 1, Role: "assistant", Provider: "anthropic", Model: "anthropic/claude", Agent: "build", CreatedAt: day1,
			TokensInput: 10, TokensOutput: 4, TokensCacheRead: 2, TokensCacheWrite: 3},
		Message{ID: "a2", Position: 2, Role: "assistant", Provider: "anthropic", Model: "anthropic/claude", CreatedAt: day2, TokensInput: 1},
		Message{ID: "x", Position: 3, Role: "unknown", CreatedAt: day2})
	// Subagent turns count toward assistant messages and models, not sessions or user messages.
	reportSession(t, d, "child", "Explore (@explore subagent)", true, day2, false,
		Message{ID: "u2", Position: 0, Role: "user", CreatedAt: day2},
		Message{ID: "a3", Position: 1, Role: "assistant", Model: "bare", CreatedAt: day2, TokensOutput: 7},
		Message{ID: "a4", Position: 2, Role: "assistant", CreatedAt: day2})
	// A Claude subagent carries no title convention; the adapter's flag decides.
	reportSession(t, d, "agent-a1", "Find where the config is loaded", true, day2, false,
		Message{ID: "u3", Position: 0, Role: "user", CreatedAt: day2})
	// A title that merely looks like the convention is not a subagent.
	reportSession(t, d, "lookalike", "Fix (flaky subagent)", false, day1, false)
	// Sessions removed from the source are retained but excluded, as analytics no longer sees them.
	reportSession(t, d, "gone", "Gone", false, day2, true,
		Message{ID: "a5", Position: 0, Role: "assistant", Model: "anthropic/claude", CreatedAt: day2, TokensInput: 100})

	days, err := d.DailyActivity(t.Context(), "first", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []DayActivity{{Day: "2026-10-01", Sessions: 2, Messages: 1, UserMessages: 1}, {Day: "2026-10-02", Messages: 3}}
	if len(days) != len(want) || days[0] != want[0] || days[1] != want[1] {
		t.Fatalf("daily activity = %+v, want %+v", days, want)
	}
	if late, err := d.DailyActivity(t.Context(), "first", day2); err != nil || len(late) != 1 || late[0].Day != "2026-10-02" {
		t.Fatalf("since filter: %+v %v", late, err)
	}

	models, err := d.ModelUsage(t.Context(), "first", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantModels := []ModelUsageRow{{Key: "anthropic/claude", Count: 2, TokensIn: 11, TokensOut: 4, CacheRead: 2, CacheWrite: 3}, {Key: "bare", Count: 1, TokensOut: 7}}
	if len(models) != len(wantModels) || models[0] != wantModels[0] || models[1] != wantModels[1] {
		t.Fatalf("model usage = %+v, want %+v", models, wantModels)
	}
	if late, err := d.ModelUsage(t.Context(), "first", day2); err != nil || len(late) != 2 || late[0].Count != 1 {
		t.Fatalf("model since filter: %+v %v", late, err)
	}
	if detail := read(t, d, Key{InstanceID: "first", SessionID: "root"}); detail.Messages[1].Agent != "build" {
		t.Fatalf("agent not stored: %+v", detail.Messages[1])
	}
}

func TestInstanceBySource(t *testing.T) {
	d := openTestDB(t)
	got, err := d.InstanceBySource(t.Context(), "owner", "opencode", "first")
	if err != nil || got.ID != "first" {
		t.Fatalf("instance lookup: %+v %v", got, err)
	}
	if _, err := d.InstanceBySource(t.Context(), "owner", "opencode", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance: %v", err)
	}
}

func TestReportQueriesUseIndexes(t *testing.T) {
	d := openTestDB(t)
	planUses(t, d, `SELECT count(*) FROM message WHERE instance_id=? AND role='assistant' AND created_at>=?`, "message_role", "first", int64(0))
	planUses(t, d, `SELECT count(*) FROM session WHERE instance_id=? AND created_at>=?`, "session_created", "first", int64(0))
}
