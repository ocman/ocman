package state

import "testing"

func BenchmarkPermissionHistoryScoped(b *testing.B) {
	d := openTestStateDB(b)
	if _, err := d.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<20000)
		INSERT INTO inbox_item(id,title,body,created_at,category,permission_json,session_json)
		SELECT 'permission-'||x,'permission','body',x,'permission',
		json_object('platform','p','sessionId',CASE WHEN x<=10 THEN 's' ELSE 'other' END,'permissionId','request-'||x),'' FROM n;
		WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100)
		INSERT INTO auto_approved_permission(platform,session_id,permission_id,permission_text,patterns_json,approved_at)
		SELECT 'p','s','fallback-'||x,'read','[]',30000+x FROM n;
		ANALYZE`); err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"indexed", "unindexed"} {
		if mode == "unindexed" {
			if _, err := d.db.Exec(`DROP INDEX inbox_permission_identity; DROP INDEX inbox_permission_invalid`); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(mode, func(b *testing.B) {
			for b.Loop() {
				history, err := d.SessionPermissionHistory(b.Context(), "p", []string{"s"})
				if err != nil || len(history) != 110 {
					b.Fatalf("history rows = %d, %v", len(history), err)
				}
			}
		})
	}
}
