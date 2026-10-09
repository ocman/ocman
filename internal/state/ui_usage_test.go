package state

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestUIUsageUnionMidnightAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 0, 1, 0, 0, time.UTC)
	midnight := now.Add(-time.Minute).UnixMilli()
	for _, tc := range []struct {
		intervals []UIUsageInterval
		want      int64
	}{
		{[]UIUsageInterval{{midnight - 30_000, midnight + 10_000}}, 40_000},
		{[]UIUsageInterval{{midnight - 20_000, midnight + 20_000}}, 10_000},
		{[]UIUsageInterval{{midnight - 30_000, midnight + 10_000}}, 0},
		{[]UIUsageInterval{{midnight + 30_000, midnight + 40_000}, {midnight + 20_000, midnight + 35_000}}, 20_000},
	} {
		got, err := d.RecordUIUsage(t.Context(), tc.intervals, now)
		if err != nil || got != tc.want {
			t.Fatalf("credited %d, want %d: %v", got, tc.want, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.RecordUIUsage(t.Context(), []UIUsageInterval{{midnight - 30_000, midnight + 40_000}}, now)
	if err != nil || got != 0 {
		t.Fatalf("restart retry = %d, %v", got, err)
	}
	days, err := d.UIUsageDays(t.Context(), now.Add(-24*time.Hour))
	want := []UIUsageDay{{"2026-10-09", 30}, {"2026-10-10", 40}}
	if err != nil || !reflect.DeepEqual(days, want) {
		t.Fatalf("days = %v, %v", days, err)
	}
	// Expired dedup intervals disappear, daily history remains.
	if _, err := d.RecordUIUsage(t.Context(), []UIUsageInterval{{midnight + 300_000, midnight + 310_000}}, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.db.QueryRow(`SELECT count(*) FROM ui_usage_interval`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retained %d, %v", count, err)
	}
}

func TestUIUsageValidationAndFailure(t *testing.T) {
	d := openTestStateDB(t)
	now := time.Unix(1000, 0)
	end := now.UnixMilli()
	for _, intervals := range [][]UIUsageInterval{
		{{end - 120_001, end}}, {{end, end + 5_001}}, {{end, end}}, {{end, end - 1}}, make([]UIUsageInterval, 65),
	} {
		if _, err := d.RecordUIUsage(t.Context(), intervals, now); err == nil {
			t.Fatalf("accepted %v", intervals)
		}
	}
	if value, err := d.RecordUIUsage(t.Context(), nil, now); err != nil || value != 0 {
		t.Fatal(value, err)
	}
	if value, err := d.RecordUIUsage(t.Context(), []UIUsageInterval{{end - 1000, end + 1000}}, now); err != nil || value != 1000 {
		t.Fatal(value, err)
	}
	if _, err := d.db.Exec(`DROP TABLE ui_usage_daily`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecordUIUsage(t.Context(), []UIUsageInterval{{end - 2000, end - 1000}}, now); err == nil {
		t.Fatal("expected transaction failure")
	}
	if _, err := d.UIUsageDays(t.Context(), now); err == nil {
		t.Fatal("expected read failure")
	}
}
