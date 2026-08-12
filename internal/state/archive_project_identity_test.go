package state

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// A project's identity is (remoteID, root): two machines routinely hold the
// same absolute path (/home/u/app on the laptop and on the build box).
// Archive state keyed by path alone hid both, and activity on one
// auto-unarchived the other.
func TestArchiveProject_IsHostQualified(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	const root = "/home/u/app"
	if err := db.ArchiveProject("local", root); err != nil {
		t.Fatalf("ArchiveProject local: %v", err)
	}

	archived, err := db.ArchivedProjects()
	if err != nil {
		t.Fatalf("ArchivedProjects: %v", err)
	}
	if _, ok := archived[ProjectKey{RemoteID: "local", Root: root}]; !ok {
		t.Fatalf("archived = %v, want the local project", archived)
	}
	if _, ok := archived[ProjectKey{RemoteID: "r-A", Root: root}]; ok {
		t.Fatalf("archived = %v, want the same path on another host untouched", archived)
	}

	// Archiving the remote's copy is a separate row, not an upsert.
	if err := db.ArchiveProject("r-A", root); err != nil {
		t.Fatalf("ArchiveProject remote: %v", err)
	}
	archived, _ = db.ArchivedProjects()
	if len(archived) != 2 {
		t.Fatalf("archived = %v, want one row per host", archived)
	}

	// Unarchiving one host leaves the other archived, and records the
	// unarchive intent against that host only.
	if err := db.UnarchiveProject("local", root); err != nil {
		t.Fatalf("UnarchiveProject: %v", err)
	}
	archived, _ = db.ArchivedProjects()
	if len(archived) != 1 {
		t.Fatalf("archived = %v, want only the remote still archived", archived)
	}
	if _, ok := archived[ProjectKey{RemoteID: "r-A", Root: root}]; !ok {
		t.Fatalf("archived = %v, want the remote row", archived)
	}

	keep, err := db.ProjectsUnarchivedSince(0)
	if err != nil {
		t.Fatalf("ProjectsUnarchivedSince: %v", err)
	}
	if !keep[ProjectKey{RemoteID: "local", Root: root}] {
		t.Fatalf("unarchive intent = %v, want the local project", keep)
	}
	if keep[ProjectKey{RemoteID: "r-A", Root: root}] {
		t.Fatalf("unarchive intent = %v, want it scoped to the local host", keep)
	}
}

// The migration adds remote_id to the project archive and unarchive-intent
// tables, backfilling every pre-existing row as 'local' (a database written
// before multi-remote can only describe the hub's own machine).
func TestMigrateV42BackfillsProjectRemoteIDAsLocal(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV19(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV38(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
		INSERT INTO archived_project (project_root, archived_at) VALUES ('/home/u/app', 111);
		INSERT INTO unarchived_entity (kind, entity_key, unarchived_at) VALUES ('project', '/home/u/other', 222);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(
		`INSERT INTO unarchived_entity (kind, entity_key, unarchived_at) VALUES ('session', ?, 333)`,
		sessionKey("opencode", "s1"),
	); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV42(tx); err != nil {
		t.Fatalf("migrateToV42: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var remoteID string
	var archivedAt int64
	if err := raw.QueryRow(`SELECT remote_id, archived_at FROM archived_project WHERE project_root = '/home/u/app'`).
		Scan(&remoteID, &archivedAt); err != nil {
		t.Fatalf("reading backfilled archived_project: %v", err)
	}
	if remoteID != "local" || archivedAt != 111 {
		t.Fatalf("archived_project row = (%q, %d), want ('local', 111)", remoteID, archivedAt)
	}

	rows := map[string]string{}
	cursor, err := raw.Query(`SELECT kind, remote_id FROM unarchived_entity`)
	if err != nil {
		t.Fatalf("reading backfilled unarchived_entity: %v", err)
	}
	for cursor.Next() {
		var kind, rid string
		if err := cursor.Scan(&kind, &rid); err != nil {
			t.Fatal(err)
		}
		rows[kind] = rid
	}
	cursor.Close()
	if rows["project"] != "local" || rows["session"] != "local" {
		t.Fatalf("unarchived_entity remote ids = %v, want everything backfilled as local", rows)
	}

	// Both tables must now hold one row per host for the same key.
	if _, err := raw.Exec(`
		INSERT INTO archived_project (remote_id, project_root, archived_at) VALUES ('r-A', '/home/u/app', 444);
		INSERT INTO unarchived_entity (kind, remote_id, entity_key, unarchived_at) VALUES ('project', 'r-A', '/home/u/other', 555);
	`); err != nil {
		t.Fatalf("per-host rows rejected: %v", err)
	}
}

// A full Open() on a pre-v42 database migrates it, and a second Open is a
// no-op: the migration is versioned, not re-run.
func TestMigrateV42IsIdempotentThroughOpen(t *testing.T) {
	path := t.TempDir() + "/state.db"
	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := first.ArchiveProject("r-A", "/home/u/app"); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	archived, err := second.ArchivedProjects()
	if err != nil {
		t.Fatalf("ArchivedProjects: %v", err)
	}
	if _, ok := archived[ProjectKey{RemoteID: "r-A", Root: "/home/u/app"}]; !ok {
		t.Fatalf("archived = %v, want the remote row to survive a reopen", archived)
	}
}
