package sidebar

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func largeInventory(t *testing.T) (*Reader, *state.DB, *statev2.DB) {
	t.Helper()
	corePath := statetest.Path(t, sessioncatalog.Schema)
	core, err := state.Open(corePath, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	history := statev2test.Open(t)
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "instance", OwnerID: "owner", Platform: "test", SourceKey: "source"}); err != nil {
		t.Fatal(err)
	}
	// Seed a realistic inventory size without running native agents or importing
	// payloads. Write behavior is covered by the ingestion/projection tests.
	_, err = core.SQL().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000)
	 INSERT INTO session_catalog(id,owner_id,platform,directory,title,origin,origin_id,creation_state,source_key,native_id,created_at,updated_at)
	 SELECT printf('ocm_%08d-0000-0000-0000-%012d',x,x),'owner','test',CASE WHEN x%2=0 THEN '/one' ELSE '/two' END,'title',
	 CASE WHEN x%10=0 THEN 'routine' ELSE 'external' END,CASE WHEN x%10=0 THEN 'r' ELSE '' END,'ready','source',printf('native-%d',x),x,x FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000)
	 INSERT INTO session(instance_id,id,title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent)
	 SELECT 'instance',printf('ocm_%08d-0000-0000-0000-%012d',x,x),'title',CASE WHEN x%2=0 THEN '/one' ELSE '/two' END,x,x,0,1,'{}','','project',1,0 FROM n;
	 INSERT INTO session_sidebar SELECT instance_id,id,title,directory,project_id,parent_id,created_at,updated_at,updated_at,source_deleted,is_subagent,0,directory,'','assistant','stop','' FROM session`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(history.FilePath(), corePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, core, history
}

func TestSidebarLargeInventoryFiltersBeforePaging(t *testing.T) {
	r, _, history := largeInventory(t)
	if err := history.SetSidebarBranch(t.Context(), "owner", "/one", "work", 100); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rows, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}, platforms.SidebarQuery{Directory: "/one", Origin: "routine", OriginID: "r", Limit: 50, Offset: 50})
	if err != nil || len(rows) != 50 || rows[0].TimeUpdated != 9500 || rows[0].Branch != "work" || rows[0].RoutineID != "r" {
		t.Fatalf("page=%+v err=%v", rows, err)
	}
	t.Logf("read 50 filtered rows from 10,000 sessions in %s", time.Since(start))
	for _, scope := range []Scope{{Owner: "other", Platform: "test"}, {Owner: "owner", Platform: "other"}} {
		if rows, err := r.Sessions(t.Context(), scope, platforms.SidebarQuery{}); err != nil || len(rows) != 0 {
			t.Fatalf("scope escaped: %+v %v", rows, err)
		}
	}
	for _, q := range []platforms.SidebarQuery{{Limit: -1}, {Offset: -1}} {
		if _, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test"}, q); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	if _, err := r.SessionsFor(t.Context(), []Scope{{Owner: "owner", Platform: "test"}, {Owner: "other", Platform: "test"}}, platforms.SidebarQuery{}); err == nil {
		t.Fatal("mixed owners accepted")
	}
	if rows, err := r.SessionsFor(t.Context(), nil, platforms.SidebarQuery{}); err != nil || len(rows) != 0 {
		t.Fatalf("empty scope=%v %v", rows, err)
	}
}

// A subagent of a routine run carries the routine like its parent, so the
// routine filter hides it alongside the run.
func TestSidebarRoutineChildInheritsRoutine(t *testing.T) {
	r, core, _ := largeInventory(t)
	// Session 1 is external; session 10 is a routine run of "r".
	if _, err := core.SQL().Exec(`UPDATE session_catalog SET parent_id='ocm_00000010-0000-0000-0000-000000000010' WHERE id='ocm_00000001-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	rows, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}, platforms.SidebarQuery{Directory: "/two"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, row := range rows {
		got[row.ID] = row.RoutineID
	}
	if child, plain := got["ocm_00000001-0000-0000-0000-000000000001"], got["ocm_00000003-0000-0000-0000-000000000003"]; child != "r" || plain != "" {
		t.Fatalf("child/plain routine = %q/%q, want r/empty", child, plain)
	}
}

func TestSidebarConnectionsCannotModifyEitherDatabase(t *testing.T) {
	r, _, _ := largeInventory(t)
	for _, query := range []string{`DELETE FROM main.session_sidebar`, `DELETE FROM core.session_catalog`} {
		if _, err := r.db.ExecContext(t.Context(), query); err == nil {
			t.Fatalf("read-only reader allowed %s", query)
		}
	}
	connections := []*sql.Conn{}
	for range 4 {
		c, err := r.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, c)
		var n int
		if err := c.QueryRowContext(t.Context(), `SELECT count(*) FROM core.session_catalog`).Scan(&n); err != nil || n != 10000 {
			t.Fatalf("pooled attachment=%d %v", n, err)
		}
	}
	for _, c := range connections {
		_ = c.Close()
	}
	if _, err := Open("", ""); err == nil {
		t.Fatal("implicit database paths accepted")
	}
	if r, err := Open(filepath.Join(t.TempDir(), "missing.db"), "missing-core.db"); err == nil {
		_ = r.Close()
		t.Fatal("read-only open created a missing history store")
	}
}
