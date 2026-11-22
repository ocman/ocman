package statev2

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func BenchmarkSessionSearch(b *testing.B) {
	d := benchmarkSearchFixture(b)
	b.Logf("index allocation: %.1f MiB (100,000 synthetic text parts)", searchIndexAllocationMiB(b, d))
	for _, tc := range []struct {
		name, query string
		count       int
	}{{"rare", "needle with spaces", 10}, {"no-match", "unknown phrase", 0}, {"common", "application", 20}} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkSearchLatency(b, func() {
				got, err := d.SearchSessionText(b.Context(), "owner", tc.query)
				if err != nil || len(got) != tc.count {
					b.Fatalf("got %d results, %v; want %d", len(got), err, tc.count)
				}
			})
		})
	}
}

func searchIndexAllocationMiB(b *testing.B, d *DB) float64 {
	b.Helper()
	tx, err := d.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	pragma := func(name string) int {
		var value int
		if err := tx.QueryRow("PRAGMA " + name).Scan(&value); err != nil {
			b.Fatal(err)
		}
		return value
	}
	pageSize, before := pragma("page_size"), pragma("freelist_count")
	if _, err := tx.Exec(`DROP TABLE session_search_doc; DROP TABLE session_search_fts`); err != nil {
		b.Fatal(err)
	}
	return float64(pageSize*(pragma("freelist_count")-before)) / (1 << 20)
}

func benchmarkSearchFixture(b *testing.B) *DB {
	b.Helper()
	d, err := Open(b.TempDir() + "/search.db")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	if err := d.RegisterInstance(b.Context(), Instance{ID: "first", OwnerID: "owner", Platform: "opencode", SourceKey: "first"}); err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		tx, err := d.db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		populateSearchFixtureSession(b, tx, i)
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
	return d
}

func populateSearchFixtureSession(b *testing.B, tx *sql.Tx, i int) {
	b.Helper()
	defer func() {
		if b.Failed() {
			_ = tx.Rollback()
		}
	}()
	id := fmt.Sprintf("ocm_%04d", i)
	if _, err := tx.Exec(`INSERT INTO session(instance_id,id,title,directory,created_at,updated_at,source_deleted,revision,extensions)
		VALUES('first',?,'Example','/repo',0,?,0,1,'{}')`, id, i); err != nil {
		b.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO message(instance_id,session_id,id,position,role,model,created_at,completed_at,source_deleted,extensions)
		VALUES('first',?,'m',0,'assistant','',0,0,0,'{}')`, id); err != nil {
		b.Fatal(err)
	}
	const insert = `WITH RECURSIVE ids(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM ids WHERE i<99)
		INSERT INTO part(instance_id,session_id,message_id,id,position,kind,text,source_deleted,extensions,created_at)
		SELECT 'first',?,'m',printf('%03d',i),i,'text',? || CASE WHEN i=99 THEN ? ELSE '' END,0,'{}',i FROM ids`
	text := strings.Repeat("The build passed and the team discussed an update to the application. ", 12)
	var needle string
	if i%100 == 0 {
		needle = " needle with spaces"
	}
	if _, err := tx.Exec(insert, id, text, needle); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkSessionSearchSave(b *testing.B) {
	for _, name := range []string{"without-index", "with-index"} {
		b.Run(name, func(b *testing.B) {
			d, err := Open(b.TempDir() + "/search.db")
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			if err := d.RegisterInstance(b.Context(), Instance{ID: "first", OwnerID: "owner", Platform: "opencode", SourceKey: "first"}); err != nil {
				b.Fatal(err)
			}
			if name == "without-index" {
				if _, err := d.db.Exec(`DROP TRIGGER session_search_part_insert; DROP TRIGGER session_search_part_update;
					DROP TRIGGER session_search_part_delete; DROP TRIGGER session_search_message_update`); err != nil {
					b.Fatal(err)
				}
			}
			batch := Batch{Session: Session{Key: Key{"first", "session"}},
				Messages: []Message{{Role: "assistant", ConversationVisible: true}},
				Parts:    []Part{{ID: "p", Kind: "text", Text: strings.Repeat("Saved conversation text for search indexing. ", 20)}}}
			benchmarkSearchLatency(b, func() {
				id := fmt.Sprint(batch.ExpectedRevision)
				batch.Messages[0].ID, batch.Parts[0].MessageID = id, id
				result, err := d.Commit(b.Context(), batch)
				if err != nil {
					b.Fatal(err)
				}
				batch.ExpectedRevision = result.Revision
			})
		})
	}
}

func benchmarkSearchLatency(b *testing.B, run func()) {
	times := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		run()
		times = append(times, time.Since(start))
	}
	b.StopTimer()
	slices.Sort(times)
	b.ReportMetric(float64(times[(len(times)-1)*95/100])/float64(time.Millisecond), "ms/p95")
}
