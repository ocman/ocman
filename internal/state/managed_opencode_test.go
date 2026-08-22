package state

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestMigrate_V35_CreatesManagedOpencodeTable proves v35 applies on a
// fresh DB (creating the table) and that a repeat migrate() is a no-op.
func TestMigrate_V35_CreatesManagedOpencodeTable(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}
	var count int
	if err := sqlDB.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='managed_opencode'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("managed_opencode table count = %d, want 1", count)
	}

	// repo_root is the primary key: inserting the same key twice must fail.
	if _, err := sqlDB.Exec(`INSERT INTO managed_opencode (repo_root) VALUES ('/r')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO managed_opencode (repo_root) VALUES ('/r')`); err == nil {
		t.Error("expected duplicate repo_root insert to fail")
	}

	// Idempotent: migrate() again is a no-op and preserves the row.
	if err := migrate(sqlDB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT count(*) FROM managed_opencode`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count after second migrate = %d, want 1", count)
	}
}

func TestManagedOpencode_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir + "/state.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	const repo = "/src/github.com/example/repo"

	// Absent initially.
	if _, ok, err := d.GetManagedOpencode(t.Context(), repo); err != nil || ok {
		t.Fatalf("initial Get: ok=%v err=%v; want ok=false, no error", ok, err)
	}

	launched := time.Unix(1700000000, 0)
	want := ManagedInstance{
		Endpoint:   "http://127.0.0.1:41235",
		Kind:       "native-tmux",
		RuntimeID:  "sess-name",
		PID:        4242,
		LaunchedAt: launched,
	}
	if err := d.UpsertManagedOpencode(t.Context(), repo, want, launched); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, ok, err := d.GetManagedOpencode(t.Context(), repo)
	if err != nil || !ok {
		t.Fatalf("Get after upsert: ok=%v err=%v", ok, err)
	}
	if got.Endpoint != want.Endpoint || got.Kind != want.Kind ||
		got.RuntimeID != want.RuntimeID || got.PID != want.PID ||
		!got.LaunchedAt.Equal(launched) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}

	// Upsert replaces the row for the same key.
	relaunched := time.Unix(1700000100, 0)
	want2 := ManagedInstance{Endpoint: "http://127.0.0.1:50000", Kind: "native-tmux", RuntimeID: "sess-2", PID: 99, LaunchedAt: relaunched}
	if err := d.UpsertManagedOpencode(t.Context(), repo, want2, relaunched); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _, err = d.GetManagedOpencode(t.Context(), repo)
	if err != nil {
		t.Fatalf("Get after re-upsert: %v", err)
	}
	if got.Endpoint != want2.Endpoint || got.RuntimeID != want2.RuntimeID {
		t.Fatalf("upsert did not replace: got %+v, want %+v", got, want2)
	}

	// Delete removes it; a second delete is a no-op.
	if err := d.DeleteManagedOpencode(t.Context(), repo); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := d.GetManagedOpencode(t.Context(), repo); err != nil || ok {
		t.Fatalf("Get after delete: ok=%v err=%v; want ok=false", ok, err)
	}
	if err := d.DeleteManagedOpencode(t.Context(), repo); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}
