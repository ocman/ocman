package sessioncatalog

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func backgroundInventory(t testing.TB, size, overrides int) *Store {
	t.Helper()
	s, d := openStore(t)
	_, err := d.SQL().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
	 INSERT INTO session_catalog(id,owner_id,platform,directory,title,origin,creation_state,source_key,native_id,created_at,updated_at)
	 SELECT printf('ocm_%08d-0000-0000-0000-%012d',x,x),'owner','test','/repo','title','external','ready','source',printf('native-%d',x),x,x FROM n`, size)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.SQL().Exec(`INSERT INTO session_background_lifecycle SELECT id,'task','service' FROM session_catalog WHERE updated_at<=?`, overrides)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBackgroundLifecyclePlanStartsWithSparseOverrides(t *testing.T) {
	s := backgroundInventory(t, 10000, 3)
	rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+backgroundLifecyclesQuery, "owner", "test", "source")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
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
	if len(plan) == 0 || !strings.HasPrefix(plan[0], "SCAN b") {
		t.Fatalf("background snapshots must start with sparse overrides, not retained history: %v", plan)
	}
	if len(plan) != 2 || !strings.Contains(plan[1], "(id=?)") {
		t.Fatalf("overrides must use catalogue identity lookups: %v", plan)
	}
}

func TestBackgroundLifecyclesScopeAndPurges(t *testing.T) {
	s := backgroundInventory(t, 6, 6)
	_, err := s.db.Exec(`UPDATE session_catalog SET owner_id='other' WHERE updated_at=2;
	 UPDATE session_catalog SET platform='other' WHERE updated_at=3;
	 UPDATE session_catalog SET source_key='other' WHERE updated_at=4;
	 UPDATE session_catalog SET purged=1 WHERE updated_at=5`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{"native-1": {"task": "service", "second": "task"}, "native-6": {"task": "service"}}
	id := "ocm_00000001-0000-0000-0000-000000000001"
	if err := s.SetBackgroundLifecycle(t.Context(), id, "second", "service"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBackgroundLifecycle(t.Context(), id, "second", "task"); err != nil {
		t.Fatal(err)
	}
	got, err := s.BackgroundLifecycles(t.Context(), "owner", "test", "source")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides=%v want=%v err=%v", got, want, err)
	}
	got, err = s.BackgroundLifecycles(t.Context(), "owner", "test", "missing")
	if err != nil || len(got) != 0 {
		t.Fatalf("unknown source returned overrides: %v %v", got, err)
	}
	for _, args := range [][2]string{{"", "task"}, {"task", "invalid"}} {
		if err := s.SetBackgroundLifecycle(t.Context(), id, args[0], args[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid override accepted: %v", err)
		}
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BackgroundLifecycles(t.Context(), "owner", "test", "source"); err == nil {
		t.Fatal("closed store accepted lifecycle read")
	}
}

func BenchmarkBackgroundLifecycles(b *testing.B) {
	for _, size := range []int{1000, 20000} {
		for _, overrides := range []int{0, 37} {
			b.Run(fmt.Sprintf("history%d/overrides%d", size, overrides), func(b *testing.B) {
				s := backgroundInventory(b, size, overrides)
				b.ReportAllocs()
				for b.Loop() {
					rows, err := s.BackgroundLifecycles(b.Context(), "owner", "test", "source")
					if err != nil || len(rows) != overrides {
						b.Fatalf("rows=%d err=%v", len(rows), err)
					}
				}
			})
		}
	}
}
