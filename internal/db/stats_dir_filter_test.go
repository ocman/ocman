package db

import (
	"testing"
	"time"
)

// Tests for the directory-prefix filter (`dir` argument) added across the
// five aggregation functions. The filter implements AD-7 from
// spec/stats-project-filter/architecture.md:
//
//	directory == dir  OR  directory startsWith (dir + '/')
//
// The critical edge case the implementation MUST handle correctly is
// the sibling-prefix trap: scope `/repo/foo` must NOT match `/repo/foobar`.
// We seed every test with that pair to prove it.

// seedDirFilter inserts three sessions with directories chosen to exercise
// the prefix filter:
//
//   - /repo/foo            — exact match for the scope
//   - /repo/foo/sub        — descendant of the scope (matches)
//   - /repo/foobar         — sibling-prefix (must NOT match scope=/repo/foo)
//   - /elsewhere           — unrelated (must NOT match)
//
// Each session has one assistant message with distinct token counts so the
// caller can verify which sessions were included by summing tokens.
func seedDirFilter(t *testing.T, db *DB) {
	t.Helper()
	now := time.Now().UnixMilli()
	insertSession(t, db, "s_exact", "Exact", "/repo/foo", now, now)
	insertSession(t, db, "s_desc", "Descendant", "/repo/foo/sub", now, now)
	insertSession(t, db, "s_sib", "Sibling", "/repo/foobar", now, now)
	insertSession(t, db, "s_other", "Other", "/elsewhere", now, now)

	mk := func(id, sess string, in, out int) {
		insertMessage(t, db, id, sess, now, map[string]interface{}{
			"role":       "assistant",
			"providerID": "anthropic",
			"modelID":    "opus-4.1",
			"tokens":     map[string]interface{}{"input": in, "output": out},
			"finish":     "end_turn",
			"time":       map[string]interface{}{"created": now - 1000, "completed": now},
		})
	}
	// Distinct token counts so we can detect which sessions are filtered in.
	mk("m_exact", "s_exact", 100, 0)
	mk("m_desc", "s_desc", 200, 0)
	mk("m_sib", "s_sib", 400, 0) // ← must be excluded when scope=/repo/foo
	mk("m_other", "s_other", 800, 0)
}

func TestDirectoryWhere(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{"empty dir produces empty fragment", "", ""},
		{"non-empty dir produces two-predicate fragment", "/repo/foo", "(s.directory = ? OR s.directory LIKE ?)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frag, args := directoryWhere(tc.dir)
			if frag != tc.want {
				t.Errorf("fragment = %q, want %q", frag, tc.want)
			}
			if tc.dir == "" {
				if len(args) != 0 {
					t.Errorf("args for empty dir = %#v, want empty", args)
				}
				return
			}
			if len(args) != 2 {
				t.Fatalf("args len = %d, want 2", len(args))
			}
			if args[0] != tc.dir {
				t.Errorf("args[0] = %v, want %q", args[0], tc.dir)
			}
			if args[1] != tc.dir+"/%" {
				t.Errorf("args[1] = %v, want %q", args[1], tc.dir+"/%")
			}
		})
	}
}

// --- GetMetricsDashboard ---

func TestGetMetricsDashboard_DirFilter(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedDirFilter(t, db)

	t.Run("empty dir matches everything (regression)", func(t *testing.T) {
		got, err := db.GetMetricsDashboard(MetricsDashboardOptions{
			RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
		})
		if err != nil {
			t.Fatalf("GetMetricsDashboard: %v", err)
		}
		if got.Summary.Requests != 4 {
			t.Errorf("Requests = %d, want 4", got.Summary.Requests)
		}
		if got.Summary.InputTokens != 1500 {
			t.Errorf("InputTokens = %d, want 1500 (sum of all)", got.Summary.InputTokens)
		}
	})

	t.Run("dir scope includes self and descendants but not siblings", func(t *testing.T) {
		got, err := db.GetMetricsDashboard(MetricsDashboardOptions{
			RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
			Dir: "/repo/foo",
		})
		if err != nil {
			t.Fatalf("GetMetricsDashboard: %v", err)
		}
		// Want: s_exact (100) + s_desc (200) = 300. Must EXCLUDE s_sib (400).
		if got.Summary.Requests != 2 {
			t.Errorf("Requests = %d, want 2 (exact + descendant only)", got.Summary.Requests)
		}
		if got.Summary.InputTokens != 300 {
			t.Errorf("InputTokens = %d, want 300; sibling /repo/foobar was incorrectly included", got.Summary.InputTokens)
		}
	})

	t.Run("dir scope filters the project log too", func(t *testing.T) {
		got, err := db.GetMetricsDashboard(MetricsDashboardOptions{
			RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
			Dir: "/repo/foo",
		})
		if err != nil {
			t.Fatalf("GetMetricsDashboard: %v", err)
		}
		dirs := make(map[string]bool)
		for _, p := range got.Projects {
			dirs[p.Directory] = true
		}
		if !dirs["/repo/foo"] || !dirs["/repo/foo/sub"] {
			t.Errorf("project log missing expected dirs: %#v", dirs)
		}
		if dirs["/repo/foobar"] {
			t.Errorf("project log incorrectly included sibling /repo/foobar")
		}
	})

	t.Run("dir scope with no matches yields zero rows", func(t *testing.T) {
		got, err := db.GetMetricsDashboard(MetricsDashboardOptions{
			RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
			Dir: "/nonexistent",
		})
		if err != nil {
			t.Fatalf("GetMetricsDashboard: %v", err)
		}
		if got.Summary.Requests != 0 {
			t.Errorf("Requests = %d, want 0", got.Summary.Requests)
		}
	})
}

// --- GetDailyActivity ---

func TestGetDailyActivity_DirFilter(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedDirFilter(t, db)

	// Add user messages to each session so we can verify dir-scoping
	// applies to user messages too.
	now := time.Now().UnixMilli()
	insertMessage(t, db, "u_exact", "s_exact", now, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "u_desc", "s_desc", now, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "u_sib", "s_sib", now, map[string]interface{}{"role": "user"})
	insertMessage(t, db, "u_other", "s_other", now, map[string]interface{}{"role": "user"})

	all, err := db.GetDailyActivity(0, "", "")
	if err != nil {
		t.Fatalf("GetDailyActivity (no dir): %v", err)
	}
	scoped, err := db.GetDailyActivity(0, "", "/repo/foo")
	if err != nil {
		t.Fatalf("GetDailyActivity (dir): %v", err)
	}

	// Sum messages across the full window for a coarse-but-decisive check.
	var allMsg, scopedMsg int
	var allUser, scopedUser int
	for _, d := range all {
		allMsg += d.Messages
		allUser += d.UserMessages
	}
	for _, d := range scoped {
		scopedMsg += d.Messages
		scopedUser += d.UserMessages
	}
	if allMsg != 4 {
		t.Errorf("unfiltered messages = %d, want 4", allMsg)
	}
	if scopedMsg != 2 {
		t.Errorf("scoped messages = %d, want 2 (exact + descendant; sibling /repo/foobar must be excluded)", scopedMsg)
	}
	if allUser != 4 {
		t.Errorf("unfiltered userMessages = %d, want 4", allUser)
	}
	if scopedUser != 2 {
		t.Errorf("scoped userMessages = %d, want 2 (exact + descendant; sibling /repo/foobar must be excluded)", scopedUser)
	}
}

// --- GetModelUsage ---

func TestGetModelUsage_DirFilter(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedDirFilter(t, db)

	all, err := db.GetModelUsage(0, "")
	if err != nil {
		t.Fatalf("GetModelUsage (no dir): %v", err)
	}
	if len(all) != 1 || all[0].Count != 4 {
		t.Fatalf("unfiltered model usage = %#v, want 1 row with Count=4", all)
	}

	scoped, err := db.GetModelUsage(0, "/repo/foo")
	if err != nil {
		t.Fatalf("GetModelUsage (dir): %v", err)
	}
	if len(scoped) != 1 || scoped[0].Count != 2 {
		t.Fatalf("scoped model usage = %#v, want 1 row with Count=2 (exact + descendant only)", scoped)
	}
}

// --- GetHourlyTokensByModel ---

func TestGetHourlyTokensByModel_DirFilter(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedDirFilter(t, db)

	all, err := db.GetHourlyTokensByModel(7, 0, "", "")
	if err != nil {
		t.Fatalf("GetHourlyTokensByModel (no dir): %v", err)
	}
	var allIn int64
	for _, r := range all {
		allIn += r.TokensIn
	}
	if allIn != 1500 {
		t.Errorf("unfiltered tokensIn = %d, want 1500", allIn)
	}

	scoped, err := db.GetHourlyTokensByModel(7, 0, "", "/repo/foo")
	if err != nil {
		t.Fatalf("GetHourlyTokensByModel (dir): %v", err)
	}
	var scopedIn int64
	for _, r := range scoped {
		scopedIn += r.TokensIn
	}
	if scopedIn != 300 {
		t.Errorf("scoped tokensIn = %d, want 300 (exact + descendant; sibling /repo/foobar must be excluded)", scopedIn)
	}
}

// --- GetHourlyActivity ---

func TestGetHourlyActivity_DirFilter(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedDirFilter(t, db)

	all, err := db.GetHourlyActivity(0, "")
	if err != nil {
		t.Fatalf("GetHourlyActivity (no dir): %v", err)
	}
	var allSess int
	for _, h := range all {
		allSess += h.Sessions
	}
	if allSess != 4 {
		t.Errorf("unfiltered sessions = %d, want 4", allSess)
	}

	scoped, err := db.GetHourlyActivity(0, "/repo/foo")
	if err != nil {
		t.Fatalf("GetHourlyActivity (dir): %v", err)
	}
	var scopedSess int
	for _, h := range scoped {
		scopedSess += h.Sessions
	}
	if scopedSess != 2 {
		t.Errorf("scoped sessions = %d, want 2 (exact + descendant; sibling /repo/foobar must be excluded)", scopedSess)
	}
}
