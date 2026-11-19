package mcp_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/testutil"
)

func listIDs(t *testing.T, text string) []string {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("list output %q: %v", text, err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row["id"].(string))
	}
	return ids
}

func TestSessionListFiltersByAgeAndOrigin(t *testing.T) {
	now := time.Now().UnixMilli()
	day := int64(24 * time.Hour / time.Millisecond)
	svc := &fakeSessionService{sessions: []db.Session{
		{ID: "user-new", Platform: "opencode", TimeUpdated: now},
		{ID: "plugin-new", Platform: "opencode", TimeUpdated: now, Origin: "plugin"},
		{ID: "routine-new", Platform: "opencode", TimeUpdated: now - day/2, RoutineID: "rtn_1"},
		{ID: "factory-new", Platform: "opencode", TimeUpdated: now - day/2, FactoryAttemptID: "att_1"},
		{ID: "user-old", Platform: "opencode", TimeUpdated: now - 3*day, Origin: "user"},
	}}
	srv := sessionServer(t, svc)
	for _, test := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"all", map[string]any{}, []string{"user-new", "plugin-new", "routine-new", "factory-new", "user-old"}},
		{"since", map[string]any{"since_days": 1}, []string{"user-new", "plugin-new", "routine-new", "factory-new"}},
		{"exclude automation", map[string]any{"exclude_origin": "plugin,routine"}, []string{"user-new", "factory-new", "user-old"}},
		{"select user", map[string]any{"origin": "user", "since_days": 2}, []string{"user-new"}},
		{"select routine", map[string]any{"origin": "routine, "}, []string{"routine-new"}},
		// Filters apply before the limit so automation cannot crowd out matches.
		{"limit after filter", map[string]any{"exclude_origin": "plugin", "limit": 2}, []string{"user-new", "routine-new"}},
		{"no match", map[string]any{"origin": "external"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.args["action"] = "list"
			result := testutil.CallTool(t, srv, "sessions", test.args)
			if result.IsError {
				t.Fatalf("list: %s", testutil.ResultText(result))
			}
			got := listIDs(t, testutil.ResultText(result))
			if len(got) != len(test.want) {
				t.Fatalf("ids = %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("ids = %v, want %v", got, test.want)
				}
			}
		})
	}
	for _, args := range []map[string]any{
		{"since_days": 0}, {"since_days": 366}, {"origin": "robot"}, {"exclude_origin": "user,robot"}, {"origin": 1}, {"detail": "yes"},
	} {
		args["action"] = "list"
		if result := testutil.CallTool(t, srv, "sessions", args); !result.IsError {
			t.Fatalf("args %v accepted: %s", args, testutil.ResultText(result))
		}
	}
}

func TestSessionListIsCompactByDefault(t *testing.T) {
	svc := &fakeSessionService{sessions: []db.Session{{ID: "ses_1", Platform: "opencode", Title: "Fix", Directory: "/repo", Branch: "fix", Status: db.StatusDone, Origin: "routine", RoutineID: "rtn_1", TimeCreated: 1, TimeUpdated: 2, TotalCost: 1.5, MessageCount: 9, RemoteID: "local"}}}
	srv := sessionServer(t, svc)
	var compact []map[string]any
	if err := json.Unmarshal([]byte(testutil.ResultText(testutil.CallTool(t, srv, "sessions", map[string]any{"action": "list"}))), &compact); err != nil || len(compact) != 1 {
		t.Fatalf("compact: %v %v", compact, err)
	}
	for _, key := range []string{"id", "platform", "title", "directory", "branch", "status", "origin", "routineId", "timeCreated", "timeUpdated", "archived"} {
		if _, ok := compact[0][key]; !ok {
			t.Errorf("compact row lacks %s: %v", key, compact[0])
		}
	}
	for _, key := range []string{"totalCost", "messageCount", "summaryFiles", "seenTimeUpdated"} {
		if _, ok := compact[0][key]; ok {
			t.Errorf("compact row includes %s: %v", key, compact[0])
		}
	}
	var full []map[string]any
	if err := json.Unmarshal([]byte(testutil.ResultText(testutil.CallTool(t, srv, "sessions", map[string]any{"action": "list", "detail": true}))), &full); err != nil || len(full) != 1 || full[0]["totalCost"] != 1.5 {
		t.Fatalf("detail: %v %v", full, err)
	}
}
