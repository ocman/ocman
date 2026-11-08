package sidebar

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

// Keep the visible directory fixed at 100 old sessions while unrelated history
// grows. Pair the prior global scan with the production directory query.
func BenchmarkSidebarDirectoryPage(b *testing.B) {
	for _, size := range []int{1000, 20000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			r, core, history := sidebarInventory(b, size)
			if _, err := core.SQL().Exec(`UPDATE session_catalog SET directory=CASE WHEN updated_at<=100 THEN '/cold' ELSE '/hot' END`); err != nil {
				b.Fatal(err)
			}
			conn, err := sql.Open("sqlite", history.FilePath())
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.Exec(`UPDATE session_sidebar SET directory=CASE WHEN updated_at<=100 THEN '/cold' ELSE '/hot' END`); err != nil {
				b.Fatal(err)
			}
			instance := statev2.Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
			if err := history.BeginSidebarGeneration(b.Context(), instance, "g"); err != nil {
				b.Fatal(err)
			}
			observations := map[string]statev2.SidebarObservation{}
			for i := 1; i <= 37; i++ {
				observations[fmt.Sprintf("native-%d", i)] = statev2.SidebarObservation{Status: "busy"}
			}
			if err := history.ReplaceSidebarObservations(b.Context(), instance, "g", time.Now().Add(time.Hour).UnixMilli(), observations); err != nil {
				b.Fatal(err)
			}
			r.snapshots = history.SidebarLiveJSON
			scope := []Scope{{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}}
			q := platforms.SidebarQuery{Directory: "/cold", Limit: 50}
			measure := func(statement string) time.Duration {
				start := time.Now()
				rows, err := r.sessionsFor(b.Context(), scope, q, statement)
				if err != nil || len(rows) != 50 {
					b.Fatalf("%d rows, %v", len(rows), err)
				}
				return time.Since(start)
			}
			var before, after time.Duration
			n := 0
			for b.Loop() {
				if n%2 == 0 {
					before += measure(selectSessions)
					after += measure(sessionStatement(q))
				} else {
					after += measure(sessionStatement(q))
					before += measure(selectSessions)
				}
				n++
			}
			b.ReportMetric(float64(before.Nanoseconds())/float64(n), "before-ns/query")
			b.ReportMetric(float64(after.Nanoseconds())/float64(n), "after-ns/query")
		})
	}
}
