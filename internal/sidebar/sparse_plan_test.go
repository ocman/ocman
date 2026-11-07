package sidebar

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Keep the previous, logically equivalent query available for skew comparisons.
func inventoryDrivenSQL(query string) string {
	return strings.ReplaceAll(query, "\n AND c.native_id<>''", "")
}

func sparseArgs(count bool) []any {
	raw, _ := json.Marshal([]Scope{{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}})
	args := []any{string(raw), "[]", time.Now().UnixMilli(), "owner", "", ""}
	if !count {
		args = append(args, 0, 0)
	}
	args = append(args, "", "", "", "")
	if !count {
		args = append(args, 50, 0)
	}
	return args
}

func queryPlan(t testing.TB, r *Reader, query string, args []any) []string {
	t.Helper()
	rows, err := r.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestSparseObservationPlans(t *testing.T) {
	for _, size := range []int{1000, 10000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			r, _, history := sidebarInventory(t, size)
			for _, live := range []int{0, 100} {
				seedSparseObservations(t, history.FilePath(), live)
				for _, count := range []bool{false, true} {
					query := selectSessions
					if count {
						query = countRunning
					}
					args := sparseArgs(count)
					plan := queryPlan(t, r, query, args)
					// The first recursive setup is pending-prompt propagation. Even with an
					// empty live set it previously visited every catalogue row in the source.
					setup, found := false, false
					for _, detail := range plan {
						if detail == "SETUP" {
							setup = true
							continue
						}
						if detail == "RECURSIVE STEP" {
							break
						}
						if setup && (strings.Contains(detail, "SEARCH c ") || strings.Contains(detail, "SCAN c")) {
							found = true
							if !strings.Contains(detail, "native_id=?") {
								t.Errorf("pending prompts must use native-key lookups, got %s", detail)
							}
							t.Logf("live=%d count=%v: %s", live, count, detail)
						}
					}
					if !found {
						t.Fatal("missing pending-prompt catalogue lookup in plan")
					}
				}
			}
		})
	}
}

func TestSparseQueryEquivalence(t *testing.T) {
	r, core, history := sidebarInventory(t, 1000)
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Skew the inventory: most rows are archived and deleted, while sparse
	// pinned/snoozed/included rows and native children still precede LIMIT.
	if _, err := core.SQL().Exec(`INSERT INTO archived_session(platform,session_id,session_time_updated,archived_at,snoozed_until)
 SELECT platform,id,updated_at,updated_at,CASE WHEN updated_at%71=0 THEN 9999999999999 ELSE 0 END FROM session_catalog WHERE updated_at%3<>0;
 INSERT INTO pinned_session(platform,session_id,pinned_at) SELECT platform,id,1 FROM session_catalog WHERE updated_at%67=0;
 INSERT INTO archived_project(remote_id,project_root,archived_at) VALUES ('local','/two',999999);
 UPDATE session_catalog SET parent_native_id='native-11' WHERE native_id='native-10';
 UPDATE session_catalog SET parent_native_id='native-12' WHERE native_id='native-11';
 UPDATE session_catalog SET source_key='retained' WHERE updated_at%79=0;
 UPDATE session_catalog SET purged=1 WHERE updated_at%83=0;
 UPDATE session_catalog SET creation_state='creating',source_key='',native_id='' WHERE updated_at%89=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE session_sidebar SET source_deleted=1 WHERE updated_at%3<>0;
 UPDATE session_sidebar SET is_subagent=1,source_child=updated_at%2 WHERE updated_at%5=0;
 UPDATE session_sidebar SET directory='/rare' WHERE updated_at%97=0`); err != nil {
		t.Fatal(err)
	}
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			// Statistics are maintained only in these disposable fixture databases.
			if _, err := core.SQL().Exec(`ANALYZE`); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`ANALYZE`); err != nil {
				t.Fatal(err)
			}
		}
		for _, n := range []int{0, 10, 1000} {
			seedSparseObservations(t, history.FilePath(), n)
			if n > 0 {
				// Exercise empty native IDs, mismatched generations and expired sources.
				if _, err := conn.Exec(`INSERT INTO sidebar_observation(owner_id,platform,source_key,generation,native_id,status,resets_at,pending_permission,pending_question,background_tasks,background_services)
     VALUES ('owner','test','source','g','','busy',0,1,0,0,0);
     UPDATE sidebar_observation SET generation='stale' WHERE native_id='native-7';
     INSERT OR REPLACE INTO sidebar_connection(owner_id,platform,source_key,generation,expires_at) VALUES ('owner','test','expired','g',1);
     INSERT INTO sidebar_observation(owner_id,platform,source_key,generation,native_id,status,resets_at,pending_permission,pending_question,background_tasks,background_services)
     VALUES ('owner','test','expired','g','native-3','busy',0,1,0,0,0)`); err != nil {
					t.Fatal(err)
				}
			}
			for _, count := range []bool{false, true} {
				query := selectSessions
				if count {
					query = countRunning
				}
				if inventoryDrivenSQL(query) == query {
					t.Fatal("equivalence comparison did not reconstruct the previous query")
				}
				for _, filter := range []string{"all", "directory", "rare", "origin", "attention", "include", "wrong-owner", "wrong-generation"} {
					t.Run(fmt.Sprintf("stats%v/live%d/count%v/%s", analyzed, n, count, filter), func(t *testing.T) {
						args := sparseArgs(count)
						switch filter {
						case "directory":
							args[4], args[5] = "/one", "/one"
						case "rare":
							args[4], args[5] = "/rare", "/rare"
						case "origin":
							index := 6
							if !count {
								index = 8
							}
							args[index], args[index+1], args[index+2], args[index+3] = "routine", "routine", "r", "r"
						case "attention":
							if !count {
								args[6], args[7] = 1, 999999
							}
						case "include":
							args[1] = `["ocm_00000010-0000-0000-0000-000000000010","ocm_00000002-0000-0000-0000-000000000002"]`
						case "wrong-owner":
							args[3] = "other"
						case "wrong-generation":
							args[0] = `[{"Owner":"owner","Platform":"test","Source":"source","Generation":"old"}]`
						}
						if !count {
							args[len(args)-1] = 3
						}
						got := drainQuery(t, r, query, args)
						want := drainQuery(t, r, inventoryDrivenSQL(query), args)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("result mismatch: current=%v previous=%v", got, want)
						}
						if !count {
							inventory := drainQuery(t, r, selectInventory, args)
							if !reflect.DeepEqual(got, inventory) {
								t.Fatal("recency page differs from inventory-driven joins")
							}
						}
					})
				}
			}
		}
	}
}

func drainQuery(t testing.TB, r *Reader, query string, args []any) [][]any {
	t.Helper()
	rows, err := r.db.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func seedSparseObservations(t testing.TB, historyPath string, n int) {
	t.Helper()
	conn, err := sql.Open("sqlite", historyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`INSERT OR REPLACE INTO sidebar_connection(owner_id,platform,source_key,generation,expires_at) VALUES ('owner','test','source','g',?)`, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DELETE FROM sidebar_observation;
 INSERT INTO sidebar_observation(owner_id,platform,source_key,generation,native_id,status,resets_at,pending_permission,pending_question,background_tasks,background_services)
 SELECT 'owner','test','source','g',printf('native-%d',updated_at),'busy',0,updated_at%10=0,0,0,0 FROM session_sidebar WHERE updated_at<=?`, n); err != nil {
		t.Fatal(err)
	}
}

// Isolated compact inventories with no live rows, 100 live rows, or a fully
// live inventory; every tenth observation has a pending permission prompt.
// This measures SQL plus identical result draining, not total application CPU.
func BenchmarkSparseSidebar(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("sessions%d", size), func(b *testing.B) {
			r, _, history := sidebarInventory(b, size)
			for _, n := range []int{0, 100, size} {
				seedSparseObservations(b, history.FilePath(), n)
				for _, count := range []bool{false, true} {
					query, name := selectSessions, "page50"
					if count {
						query, name = countRunning, "count"
					}
					for _, old := range []bool{true, false} {
						sqlQuery, label := query, "current"
						if old {
							sqlQuery, label = inventoryDrivenSQL(query), "previous"
						}
						b.Run(fmt.Sprintf("%s/%s/live%d", label, name, n), func(b *testing.B) {
							args := sparseArgs(count)
							b.ReportAllocs()
							for b.Loop() {
								drainQuery(b, r, sqlQuery, args)
							}
						})
					}
				}
			}
		})
	}
}

// Compare persisted statistics in a disposable import-sized fixture. This is
// evidence for future maintenance policy, not request-time or startup ANALYZE.
func BenchmarkSparseSidebarAnalyzed(b *testing.B) {
	r, core, history := sidebarInventory(b, 10000)
	seedSparseObservations(b, history.FilePath(), 100)
	if _, err := core.SQL().Exec(`ANALYZE`); err != nil {
		b.Fatal(err)
	}
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := conn.Exec(`ANALYZE`); err != nil {
		_ = conn.Close()
		b.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		b.Fatal(err)
	}
	for _, count := range []bool{false, true} {
		query, name := selectSessions, "page50"
		if count {
			query, name = countRunning, "count"
		}
		b.Run(name, func(b *testing.B) {
			args := sparseArgs(count)
			previous := inventoryDrivenSQL(query)
			var oldTime, newTime time.Duration
			iteration := 0
			measure := func(sqlQuery string) time.Duration {
				start := time.Now()
				drainQuery(b, r, sqlQuery, args)
				return time.Since(start)
			}
			for b.Loop() {
				// Alternate paired reads to reduce shared-host load/order bias.
				if iteration%2 == 0 {
					oldTime += measure(previous)
					newTime += measure(query)
				} else {
					newTime += measure(query)
					oldTime += measure(previous)
				}
				iteration++
			}
			b.ReportMetric(float64(oldTime.Nanoseconds())/float64(iteration), "previous-ns/query")
			b.ReportMetric(float64(newTime.Nanoseconds())/float64(iteration), "current-ns/query")
		})
	}
}
