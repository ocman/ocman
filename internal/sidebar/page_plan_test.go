package sidebar

import (
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func TestSidebarPageStreamsRecency(t *testing.T) {
	r, _, _ := sidebarInventory(t, 10000)
	plan := queryPlan(t, r, selectSessions, sparseArgs(false))
	if plan[len(plan)-1] == "USE TEMP B-TREE FOR ORDER BY" {
		t.Fatal("sidebar page sorts the whole inventory before LIMIT instead of streaming recency")
	}
	for _, detail := range plan {
		if detail == "SCAN s USING INDEX sidebar_global_recency" {
			return
		}
	}
	t.Fatal("sidebar page does not stream the global recency index")
}

// A quiet dashboard still polls while a small set of sessions has live state.
// Compare both SQL strategies on the same disposable history and row draining.
func BenchmarkSidebarPolling(b *testing.B) {
	r, core, history := sidebarInventory(b, 10000)
	seedSparseObservations(b, history.FilePath(), 37)
	if _, err := core.SQL().Exec(`INSERT INTO setting(key,value,updated_at)
 SELECT 'session.halt.latest.'||id,'',updated_at FROM session_catalog WHERE updated_at<=308;
 INSERT INTO seen_session(platform,session_id,session_time_updated,interrupted,seen_at)
 SELECT platform,id,updated_at,0,updated_at FROM session_catalog WHERE updated_at<=4771`); err != nil {
		b.Fatal(err)
	}
	previous := strings.Replace(selectInventory, haltsCTE, catalogueHaltCTE, 1)
	for _, page := range []struct {
		name  string
		limit int
	}{{"page50", 50}, {"page500", 500}} {
		b.Run(page.name, func(b *testing.B) {
			args := sparseArgs(false)
			args[len(args)-2] = page.limit
			var before, after time.Duration
			n := 0
			measure := func(query string) time.Duration {
				start := time.Now()
				drainQuery(b, r, query, args)
				return time.Since(start)
			}
			for b.Loop() {
				if n%2 == 0 {
					before += measure(previous)
					after += measure(selectSessions)
				} else {
					after += measure(selectSessions)
					before += measure(previous)
				}
				n++
			}
			b.ReportMetric(float64(before.Nanoseconds())/float64(n), "before-ns/query")
			b.ReportMetric(float64(after.Nanoseconds())/float64(n), "after-ns/query")
		})
	}
}

func BenchmarkSidebarPageRead(b *testing.B) {
	r, _, _ := sidebarInventory(b, 10000)
	scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	b.ReportAllocs()
	for b.Loop() {
		rows, err := r.Sessions(b.Context(), scope, platforms.SidebarQuery{Limit: 50})
		if err != nil || len(rows) != 50 {
			b.Fatalf("rows=%d err=%v", len(rows), err)
		}
	}
}
