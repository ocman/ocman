package db

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSessionConcurrency(t *testing.T) {
	for _, mirror := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "mirror"}[mirror], func(t *testing.T) {
			d := openTestDB(t)
			defer d.Close()
			hour := time.Hour.Milliseconds()
			start := int64(1000)
			insertSession(t, d, "a", "a", "/repo", start, start+4*hour)
			insertSession(t, d, "b", "b", "/repo/child", start, start+4*hour)
			insertSession(t, d, "c", "c", "/repository", start, start+4*hour)
			for _, row := range []struct {
				id, session, role string
				from, to          int64
			}{
				{"a1", "a", "assistant", start - 100, start + hour},
				{"a2", "a", "assistant", start + 100, start + hour},
				{"b1", "b", "assistant", start + 200, start + 2*hour},
				{"b2", "b", "assistant", start + 3*hour, start + 5*hour},
				{"c1", "c", "assistant", start, start + 4*hour},
				{"user", "a", "user", start, start + 4*hour},
				{"open", "a", "assistant", start, 0},
				{"invalid", "a", "assistant", start + 100, start},
			} {
				insertMessage(t, d, row.id, row.session, row.from, map[string]any{
					"role": row.role, "time": map[string]any{"created": row.from, "completed": row.to},
				})
			}
			if mirror {
				if err := d.EnableAnalyticsMirror(filepath.Join(t.TempDir(), "mirror.db"), "test"); err != nil {
					t.Fatal(err)
				}
				if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			got, err := d.GetSessionConcurrency(t.Context(), start, start+4*hour, "/repo")
			if err != nil {
				t.Fatal(err)
			}
			want := []ConcurrencyPoint{{start, 2}, {start + hour, 1}, {start + 2*hour, 0}, {start + 3*hour, 1}}
			if got.BucketMs != hour || !reflect.DeepEqual(got.Series, want) {
				t.Fatalf("got %+v, want %v", got, want)
			}
			all, err := d.GetSessionConcurrency(t.Context(), 0, start+4*hour, "")
			if err != nil || all.Series[0].Sessions != 3 || all.Series[0].Timestamp != start-100 {
				t.Fatalf("all time = %+v, %v", all, err)
			}
		})
	}
}

func TestConcurrencyEmptyAndBounded(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	for _, since := range []int64{0, 1000, 2000} {
		got, err := d.GetSessionConcurrency(t.Context(), since, 2000, "")
		if err != nil || got.Series == nil {
			t.Fatalf("empty = %+v, %v", got, err)
		}
	}
	got, err := d.GetSessionConcurrency(t.Context(), 1000, 1000+1001*time.Hour.Milliseconds(), "")
	if err != nil || len(got.Series) > 1000 || got.BucketMs != 2*time.Hour.Milliseconds() {
		t.Fatalf("bounded = %+v, %v", got, err)
	}
	if _, err := d.db.Exec(`DROP TABLE message`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetSessionConcurrency(t.Context(), 0, 2000, ""); err == nil {
		t.Fatal("expected query error")
	}
}

func TestConcurrencyTouchingIntervals(t *testing.T) {
	got := concurrencyBuckets([]concurrencyEvent{{100, 1}, {200, -1}, {200, 1}, {300, -1}}, 100, 400, 100)
	want := []ConcurrencyPoint{{100, 1}, {200, 1}, {300, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
