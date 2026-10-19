package statev2

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "statev2 ?# ü.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	commit(t, d, b)
	for pragma, want := range map[string]string{"journal_mode": "wal", "foreign_keys": "1", "synchronous": "2", "user_version": fmt.Sprint(len(migrations))} {
		var got string
		if err := d.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %q, want %q: %v", pragma, got, want, err)
		}
	}
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("insecure database file %s: %v", file, err)
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
	if got := read(t, d, b.Session.Key); got.Parts[0].Text != "retained content" || got.Session.Revision != 1 {
		t.Fatal("history lost on reopen")
	}
	if Path(filepath.Join("isolated", "state.db")) != filepath.Join("isolated", "statev2.db") {
		t.Fatal("statev2 path escaped the supplied state directory")
	}
}

func TestRejectForeignAndFutureDatabases(t *testing.T) {
	for name, setup := range map[string]string{
		"legacy":           "CREATE TABLE schema_version(version INTEGER)",
		"foreign":          "PRAGMA application_id=123",
		"unmarked version": "PRAGMA user_version=1",
		"negative":         "PRAGMA application_id=1329821234; PRAGMA user_version=-1",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "statev2.db")
			conn, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(setup); err != nil {
				t.Fatal(err)
			}
			conn.Close()
			if d, err := Open(path); err == nil {
				d.Close()
				t.Fatal("opened an incompatible database")
			}
		})
	}
}

func TestUnsupportedSchemaNeverDiscardsHistory(t *testing.T) {
	for name, version := range map[string]int{"unversioned": 0, "v1": 1, "v2": 2, "v3": 3, "v4": 4, "newer": len(migrations) + 1} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "statev2.db")
			d, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			register(t, d, "first")
			b := fixture("first")
			commit(t, d, b)
			if err := d.Purge(t.Context(), Key{InstanceID: "first", SessionID: "purged"}); err != nil {
				t.Fatal(err)
			}
			if err := d.SetSourceState(t.Context(), "first", "version/session", "retained-hint"); err != nil {
				t.Fatal(err)
			}
			if _, err := d.db.Exec(`CREATE TABLE future_extension(value TEXT); INSERT INTO future_extension VALUES('retained')`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
				t.Fatal(err)
			}
			d.Close()
			if opened, err := Open(path); err == nil {
				opened.Close()
				t.Fatal("unsupported schema was silently rebuilt instead of rejected")
			}
			conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			retained := &DB{db: conn, path: path}
			if page, err := retained.Read(t.Context(), b.Session.Key, 30, 0); err != nil || len(page.Parts) != 1 || page.Parts[0].Text != "retained content" {
				t.Fatal("unsupported schema lost conversation content", page, err)
			}
			if cursor, err := retained.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
				t.Fatal("unsupported schema lost checkpoint", cursor, err)
			}
			if _, purged, err := retained.SessionState(t.Context(), Key{InstanceID: "first", SessionID: "purged"}); err != nil || !purged {
				t.Fatal("unsupported schema lost purge marker", err)
			}
			if hints, err := retained.SourceState(t.Context(), "first"); err != nil || hints["version/session"] != "retained-hint" {
				t.Fatal("unsupported schema lost source hints", hints, err)
			}
			var extension string
			if err := conn.QueryRow(`SELECT value FROM future_extension`).Scan(&extension); err != nil || extension != "retained" {
				t.Fatal("unsupported extension table was modified", err)
			}
			var got int
			if err := conn.QueryRow("PRAGMA user_version").Scan(&got); err != nil || got != version {
				t.Fatalf("unsupported schema version changed to %d: %v", got, err)
			}
		})
	}
}

func TestSourceState(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "first")
	if err := d.SetSourceState(t.Context(), "first", "version/a", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSourceState(t.Context(), "first", "version/a", "v2"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSourceState(t.Context(), "first", "verified-at", "10"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSourceState(t.Context(), "first", "verified-at", ""); err != nil {
		t.Fatal(err)
	}
	got, err := d.SourceState(t.Context(), "first")
	if err != nil || len(got) != 1 || got["version/a"] != "v2" {
		t.Fatalf("source state: %v %v", got, err)
	}
	if other, err := d.SourceState(t.Context(), "other"); err != nil || len(other) != 0 {
		t.Fatalf("source state crossed instances: %v %v", other, err)
	}
	for _, bad := range [][2]string{{"", "k"}, {"first", ""}} {
		if err := d.SetSourceState(t.Context(), bad[0], bad[1], "v"); err == nil {
			t.Fatal("accepted source state without identity")
		}
	}
	if err := d.SetSourceState(t.Context(), "unregistered", "k", "v"); err == nil {
		t.Fatal("source state for an unregistered instance")
	}
	d.Close()
	if _, err := d.SourceState(t.Context(), "first"); err == nil {
		t.Fatal("read closed source state")
	}
}

func TestRejectUnsafePaths(t *testing.T) {
	for _, path := range []string{"", ":memory:", t.TempDir()} {
		if d, err := Open(path); err == nil {
			d.Close()
			t.Fatalf("opened invalid path %q", path)
		}
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("leave untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := filepath.Join(t.TempDir(), "statev2.db")
		if err := os.Symlink(target, path+suffix); err != nil {
			t.Fatal(err)
		}
		if d, err := Open(path); err == nil {
			d.Close()
			t.Fatalf("followed symlink %s", path+suffix)
		}
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "leave untouched" {
		t.Fatal("symlink target modified")
	}
	if d, err := Open(filepath.Join(target, "statev2.db")); err == nil {
		d.Close()
		t.Fatal("opened database under a regular file")
	}
	linkedDir := filepath.Join(root, "linked-dir")
	if err := os.Symlink(t.TempDir(), linkedDir); err != nil {
		t.Fatal(err)
	}
	if d, err := Open(filepath.Join(linkedDir, "statev2.db")); err == nil {
		d.Close()
		t.Fatal("followed symlink database directory")
	}
}

func TestClosedDatabaseErrors(t *testing.T) {
	d := openTestDB(t)
	d.Close()
	b := fixture("first")
	if _, err := d.Commit(t.Context(), b); err == nil {
		t.Fatal("committed to closed database")
	}
	if _, err := d.Read(t.Context(), b.Session.Key, 1, 0); err == nil {
		t.Fatal("read closed database")
	}
	if _, err := d.Changes(t.Context(), 0, 1); err == nil {
		t.Fatal("read closed outbox")
	}
	if _, err := d.Checkpoint(t.Context(), "first", "s"); err == nil {
		t.Fatal("read closed checkpoint")
	}
	if err := d.RegisterInstance(t.Context(), Instance{ID: "i", OwnerID: "o", Platform: "p", SourceKey: "s"}); err == nil {
		t.Fatal("registered in closed database")
	}
	if _, err := d.Instance(t.Context(), "first"); err == nil {
		t.Fatal("read closed instance")
	}
	if err := d.Purge(t.Context(), b.Session.Key); err == nil {
		t.Fatal("purged closed database")
	}
	if err := d.migrate(); err == nil {
		t.Fatal("migrated closed database")
	}
}
