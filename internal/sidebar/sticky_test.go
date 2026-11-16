package sidebar

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// A sticky first page returns exactly the unbounded inventory's newest rows
// plus every older row that must stay visible, in the same order.
func TestStickyPageKeepsVisibleRowsBeyondLimit(t *testing.T) {
	r, history, scopes := parityFixture(t)
	old := "ocm_00000013-0000-0000-0000-000000000013"
	// Visible rows matching no other sticky rule, in /two and /one.
	plainTwo, plainOne := "ocm_00000023-0000-0000-0000-000000000023", "ocm_00000038-0000-0000-0000-000000000038"
	beyond := 0
	for _, live := range []bool{false, true} {
		reader := *r
		if live {
			reader.snapshots = history.SidebarLiveJSON
		}
		for i, scope := range scopes {
			for j, q := range []platforms.SidebarQuery{
				{Limit: 10},
				{Limit: 10, Since: 150},
				{Limit: 10, IncludeIDs: []string{old}},
				{Limit: 10, Directory: "/one"},
				{Limit: 10, Origin: "routine", OriginID: "r"},
				{Limit: 500},
				{Limit: 10, StickyIDs: []string{plainTwo}},
				{Limit: 10, Directory: "/one", StickyIDs: []string{plainTwo, plainOne}},
			} {
				t.Run(fmt.Sprintf("live=%v/scope=%d/query=%d", live, i, j), func(t *testing.T) {
					all := q
					all.Limit = 0
					inventory, err := reader.sessionsFor(t.Context(), scope, all, selectInventory)
					if err != nil {
						t.Fatal(err)
					}
					var want []string
					for index, row := range inventory {
						if index < q.Limit || stickyRow(row, append(slices.Clone(q.IncludeIDs), q.StickyIDs...)) {
							want = append(want, row.ID)
							beyond += min(1, max(0, index-q.Limit+1))
						}
					}
					q.Sticky = true
					rows, err := reader.SessionsFor(t.Context(), scope, q)
					if err != nil {
						t.Fatal(err)
					}
					got := make([]string, len(rows))
					for k, row := range rows {
						got[k] = row.ID
					}
					if !slices.Equal(want, got) {
						t.Fatalf("sticky page:\n got %v\nwant %v", got, want)
					}
					if q.Directory != "" && slices.Contains(got, plainTwo) {
						t.Fatalf("sticky identity bypassed the directory filter: %v", got)
					}
				})
			}
		}
	}
	if beyond == 0 {
		t.Fatal("fixture has no sticky rows beyond the limit")
	}
}

func stickyRow(row db.Session, included []string) bool {
	return row.PinnedAt > 0 || row.SnoozedUntil > 0 || row.Status == db.StatusBusy && row.LiveConnection ||
		row.PendingPermission || row.PendingQuestion || row.BackgroundTaskCount > 0 || row.BackgroundServiceCount > 0 ||
		slices.Contains(included, row.ID)
}

func TestStickyPageIgnoresLaterPages(t *testing.T) {
	r, _, scopes := parityFixture(t)
	q := platforms.SidebarQuery{Limit: 10, Offset: 10}
	want, err := r.SessionsFor(t.Context(), scopes[0], q)
	if err != nil {
		t.Fatal(err)
	}
	q.Sticky = true
	got, err := r.SessionsFor(t.Context(), scopes[0], q)
	if err != nil || !slices.EqualFunc(want, got, func(a, b db.Session) bool { return a.ID == b.ID }) {
		t.Fatalf("later sticky page = %d rows, want %d: %v", len(got), len(want), err)
	}
}

// Single rows and sticky rows start at their catalogue identities. Walking
// the recency index or every catalogue row of the owner scales with history.
func TestIdentityReadsSeekRequestedRows(t *testing.T) {
	r, _, _ := sidebarInventory(t, 2000)
	for name, statement := range map[string]string{"session": singleSessionQuery(), "sticky": selectSticky} {
		args := sparseArgs(false)
		args[1] = `["ocm_00000100-0000-0000-0000-000000000100"]`
		args[len(args)-2] = -1
		if statement == selectSticky {
			args = slices.Insert(args, 3, any(`["ocm_00000200-0000-0000-0000-000000000200"]`))
		}
		plan := strings.Join(queryPlan(t, r, statement, args), "\n")
		for _, scan := range []string{"SCAN s ", "session_catalog_legacy_binding (owner_id=?)"} {
			if strings.Contains(plan, scan) {
				t.Errorf("%s read uses %q:\n%s", name, scan, plan)
			}
		}
	}
}

// BenchmarkSidebarIdentityReads compares the previous and current statements
// on retained history shaped like a long-lived install: 20,000 sessions ten
// minutes apart, 90% archived, sparse pins and a live connection.
func BenchmarkSidebarIdentityReads(b *testing.B) {
	const size = 20000
	r, core, history := sidebarInventory(b, size)
	now := time.Now().UnixMilli()
	const step = 10 * 60 * 1000
	age := `?-(?-CAST(substr(id,5,8) AS INTEGER))*?`
	if _, err := core.SQL().Exec(`UPDATE session_catalog SET updated_at=`+age+`;
	 INSERT INTO archived_session(platform,session_id,session_time_updated,archived_at) SELECT platform,id,updated_at,updated_at FROM session_catalog WHERE CAST(substr(id,5,8) AS INTEGER)%10<>0;
	 INSERT INTO pinned_session(platform,session_id,pinned_at) SELECT platform,id,1 FROM session_catalog WHERE CAST(substr(id,5,8) AS INTEGER)%997=0`,
		now, size, step); err != nil {
		b.Fatal(err)
	}
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE session_sidebar SET updated_at=`+age+`, last_message_at=updated_at`, now, size, step); err != nil {
		b.Fatal(err)
	}
	_ = conn.Close()
	seedSparseObservations(b, history.FilePath(), 0)
	scope := []Scope{{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}}
	since := now - 72*3600*1000
	previousSingle := strings.ReplaceAll(strings.Replace(selectSessions, "WHERE i.owner_id=?", "WHERE s.id IN requested AND i.owner_id=?", 1), " OR s.id IN requested", "")
	id := fmt.Sprintf("ocm_%08d-0000-0000-0000-%012d", size-5, size-5)
	for _, c := range []struct {
		name string
		read func() ([]db.Session, error)
	}{
		{"sidebar/unbounded", func() ([]db.Session, error) {
			return r.SessionsFor(b.Context(), scope, platforms.SidebarQuery{Since: since})
		}},
		{"sidebar/sticky500", func() ([]db.Session, error) {
			return r.SessionsFor(b.Context(), scope, platforms.SidebarQuery{Since: since, Limit: 500, Sticky: true})
		}},
		{"single/previous", func() ([]db.Session, error) {
			return r.sessionsFor(b.Context(), scope, platforms.SidebarQuery{IncludeIDs: []string{id}}, previousSingle)
		}},
		{"single/identity", func() ([]db.Session, error) {
			return r.sessionsFor(b.Context(), scope, platforms.SidebarQuery{IncludeIDs: []string{id}}, singleSessionQuery())
		}},
	} {
		b.Run(c.name, func(b *testing.B) {
			var rows []db.Session
			for b.Loop() {
				if rows, err = c.read(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(rows)), "rows")
		})
	}
}
