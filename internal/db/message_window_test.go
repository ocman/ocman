package db

import (
	"strings"
	"testing"
	"time"
)

// TestMessagesFromQueryPlan pins the plan: a recent window must range-scan
// OpenCode's (session_id, time_created) index instead of reading every
// message blob, while old/all-time windows keep the cheaper full scan.
func TestMessagesFromQueryPlan(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	if _, err := d.db.Exec(`CREATE INDEX message_session_time_created_id_idx ON message (session_id, time_created, id)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tests := []struct {
		name  string
		since int64
		want  string
	}{
		{"7 days", now.AddDate(0, 0, -7).UnixMilli(), "SEARCH m USING INDEX message_session_time_created_id_idx"},
		{"30 days", now.AddDate(0, 0, -30).UnixMilli(), "SEARCH m USING INDEX message_session_time_created_id_idx"},
		{"90 days", now.AddDate(0, 0, -90).UnixMilli(), "SCAN m"},
		{"all time", 0, "SCAN m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := d.db.Query(`EXPLAIN QUERY PLAN SELECT m.id FROM `+messagesFrom(tt.since, true)+
				` WHERE json_extract(m.data, '$.role') = 'assistant' AND m.time_created >= ?`, tt.since)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if got := strings.Join(plan, "; "); !strings.Contains(got, tt.want) {
				t.Fatalf("plan %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// TestMessagesFromKeepsWindowRows checks the indexed path returns the same
// rows as the scan: in-window messages only, across sessions.
func TestMessagesFromKeepsWindowRows(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	now := time.Now().UnixMilli()
	insertSession(t, d, "a", "A", "/a", now, now)
	insertSession(t, d, "b", "B", "/b", now, now)
	insertMessage(t, d, "old", "a", now-40*24*3600*1000, map[string]any{"role": "assistant"})
	insertMessage(t, d, "a1", "a", now-1000, map[string]any{"role": "assistant"})
	insertMessage(t, d, "b1", "b", now-2000, map[string]any{"role": "assistant"})
	insertMessage(t, d, "u1", "b", now-3000, map[string]any{"role": "user"})

	since := now - 7*24*3600*1000
	var n int
	err := d.db.QueryRow(`SELECT count(*) FROM `+messagesFrom(since, false)+
		` WHERE json_extract(m.data, '$.role') = 'assistant' AND m.time_created >= ?`, since).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}
