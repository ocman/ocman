package db

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAgentRunHoursIncludesToolsExcludesWaits(t *testing.T) {
	for _, mirror := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "mirror"}[mirror], func(t *testing.T) {
			d := openTestDB(t)
			defer d.Close()
			hour := time.Hour.Milliseconds()
			start := hour
			insertSession(t, d, "a", "main", "/repo", start, start+3*hour)
			insertSession(t, d, "b", "child", "/repo/child", start, start+3*hour)
			insertSession(t, d, "other", "other", "/repository", start, start+3*hour)
			for _, row := range []struct {
				id, session, role string
				start, end        int64
			}{
				{"a1", "a", "assistant", start, start + 2*hour},
				{"a2", "a", "assistant", start + hour/2, start + hour},
				{"b1", "b", "assistant", start, start + hour},
				{"c1", "other", "assistant", start, start + hour},
				{"user", "a", "user", start, start + hour},
				{"open", "a", "assistant", start + 2*hour, 0},
			} {
				insertMessage(t, d, row.id, row.session, row.start, map[string]any{"role": row.role, "time": map[string]any{"created": row.start, "completed": row.end}})
			}
			insertPart(t, d, "question", "a1", "a", start, map[string]any{
				"type": "tool", "tool": "question", "state": map[string]any{"time": map[string]any{"start": start + hour/2, "end": start + hour}},
			})
			// Tool execution counts, even when it occupies the whole assistant interval.
			insertPart(t, d, "bash", "b1", "b", start, map[string]any{
				"type": "tool", "tool": "bash", "state": map[string]any{"time": map[string]any{"start": start, "end": start + hour}},
			})
			if mirror {
				if err := d.EnableAnalyticsMirror(filepath.Join(t.TempDir(), "mirror.db"), "test"); err != nil {
					t.Fatal(err)
				}
				if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := d.db.Exec(`DROP TABLE part`); err != nil {
					t.Fatal(err)
				}
			}
			// Permission overlaps the question: subtract its union, not twice.
			waits := map[string][]RunInterval{"a": {{start + hour/4, start + hour/2 + hour/4}, {start + hour + hour/2, start + 2*hour}}}
			got, err := d.GetAgentRunHours(t.Context(), start, start+3*hour, "/repo", waits)
			want := []AgentRunHour{{start, 75}, {start + hour, 30}, {start + 2*hour, 0}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("hours = %v, %v; want %v", got, err, want)
			}
			if len(waits["a"]) != 2 {
				t.Fatal("mutated caller waits")
			}
			clipped, err := d.GetAgentRunHours(t.Context(), start+hour/2, start+hour+hour/2, "/repo", waits)
			if err != nil || !reflect.DeepEqual(clipped, []AgentRunHour{{start, 30}, {start + hour, 30}}) {
				t.Fatalf("clipped = %v, %v", clipped, err)
			}
		})
	}
}

func TestAgentRunBucketsCrossHoursAndEmpty(t *testing.T) {
	hour := time.Hour.Milliseconds()
	runs := map[string][]RunInterval{"a": {{hour + hour/2, 2*hour + hour/2}, {3 * hour, 3 * hour}}}
	waits := map[string][]RunInterval{"a": {{0, hour}, {2 * hour, 2*hour + hour/4}, {2*hour + hour/8, 2*hour + hour/4}}}
	got := agentRunBuckets(runs, waits, 0, 3*hour)
	want := []AgentRunHour{{hour, 30}, {2 * hour, 15}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := agentRunBuckets(nil, nil, 0, 3*hour); got == nil || len(got) != 0 {
		t.Fatal(got)
	}
	if got := agentRunBuckets(nil, nil, hour, 3*hour); !reflect.DeepEqual(got, []AgentRunHour{{hour, 0}, {2 * hour, 0}}) {
		t.Fatal(got)
	}
}
