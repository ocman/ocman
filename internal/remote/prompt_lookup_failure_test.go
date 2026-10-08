package remote

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

func TestPromptLookupFailureCrossesRealRemoteRPC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE session (
		id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, title TEXT,
		directory TEXT, time_created INTEGER, time_updated INTEGER,
		summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, share_url TEXT
	); INSERT INTO session VALUES ('parent', 'p', NULL, 'Parent', '/isolated-prompt-test', 1, 2, 0, 0, 0, '');
	INSERT INTO session VALUES ('child', 'p', 'parent', 'Child', '/isolated-prompt-test', 1, 2, 0, 0, 0, '')`); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	adapter := opencode.New(database, nil)
	adapter.ObservePromptAsked("", "/isolated-prompt-test", "permission", platforms.LivePrompt{"id": "p1", "sessionID": "child"})
	adapter.ObservePromptAsked("", "/isolated-prompt-test", "question", platforms.LivePrompt{"id": "q1", "sessionID": "child"})
	reg := platforms.NewRegistry()
	reg.Register(adapter)
	conn := NewRemoteConn(startRealRemote(t, "tok", "rid", reg), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	remote := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	if prompts, err := remote.ListPermissions(t.Context(), "parent"); err != nil || len(prompts) != 1 {
		t.Fatalf("healthy child permission=%+v error=%v", prompts, err)
	}
	// Simulate an owner DB lookup failure after the session fan-out succeeded.
	if _, err := raw.Exec(`DROP TABLE session`); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ListPermissions(t.Context(), "parent"); err == nil {
		t.Fatal("owner ancestor failure became an authoritative empty permission snapshot")
	}
	if _, err := remote.ListQuestions(t.Context(), "parent"); err == nil {
		t.Fatal("owner ancestor failure became an authoritative empty question snapshot")
	}
}
