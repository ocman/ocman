package sidebar

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

// Synthetic, isolated modernc workload: 10,000 sessions, 660,000 messages,
// 100 live busy observations. This measures query costs, not total app CPU.
func BenchmarkSidebarHistory(b *testing.B) {
	r, _, history := largeInventory(b)
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<66)
	 INSERT INTO message(instance_id,session_id,id,position,role,model,created_at,completed_at,source_deleted,extensions)
	 SELECT s.instance_id,s.id,printf('message-%d',x),x,'assistant','',x,x,0,'{}' FROM session s CROSS JOIN n`)
	if err != nil {
		b.Fatal(err)
	}
	scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	instance := statev2.Instance{OwnerID: scope.Owner, Platform: scope.Platform, SourceKey: scope.Source}
	if err := history.BeginSidebarGeneration(b.Context(), instance, "g"); err != nil {
		b.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO sidebar_connection(owner_id,platform,source_key,generation,expires_at) VALUES ('owner','test','source','g',?)
	 ON CONFLICT(owner_id,platform,source_key) DO UPDATE SET generation='g',expires_at=excluded.expires_at`, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		b.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO sidebar_observation(owner_id,platform,source_key,generation,native_id,status,resets_at,pending_permission,pending_question,background_tasks,background_services)
	 SELECT 'owner','test','source','g',printf('native-%d',updated_at),'busy',0,0,0,0,0 FROM session WHERE updated_at<=100`); err != nil {
		b.Fatal(err)
	}
	// Reconstruct the removed correlated expression for a before/after query
	// comparison with identical row draining on the same migrated modernc store.
	legacy := strings.Replace(selectSessions, "\n FROM main.session_sidebar", `,
 CASE WHEN COALESCE(v.session_time_updated,0)>=s.updated_at THEN 0 ELSE
 (SELECT count(*) FROM main.message m WHERE m.instance_id=s.instance_id AND m.session_id=s.id AND m.source_deleted=0 AND m.created_at>COALESCE(v.session_time_updated,0)) END
  FROM main.session_sidebar`, 1)
	if legacy == selectSessions {
		b.Fatal("legacy benchmark did not restore the unread subquery")
	}
	raw, _ := json.Marshal([]Scope{scope})
	for _, tc := range []struct {
		name, query string
		limit       int
	}{
		{"old-all-with-unread", legacy, -1},
		{"compact-all", selectSessions, -1},
		{"old-page50-with-unread", legacy, 50},
		{"compact-page50", selectSessions, 50},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				rows, err := r.db.QueryContext(b.Context(), tc.query, string(raw), "[]", time.Now().UnixMilli(), scope.Owner, "", "", 0, 0, "", "", "", "", tc.limit, 0)
				if err != nil {
					b.Fatal(err)
				}
				columns, _ := rows.Columns()
				values := make([]any, len(columns))
				for i := range values {
					values[i] = new(any)
				}
				for rows.Next() {
					if err := rows.Scan(values...); err != nil {
						b.Fatal(err)
					}
				}
				if err := rows.Err(); err != nil {
					b.Fatal(err)
				}
				_ = rows.Close()
			}
		})
	}
	b.Run("running-aggregate", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if n, err := r.RunningCount(b.Context(), []Scope{scope}, platforms.SidebarQuery{}); err != nil || n != 100 {
				b.Fatalf("count=%d err=%v", n, err)
			}
		}
	})
}
