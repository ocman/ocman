package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

func TestHistoryInventoryIsNewestFirst(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	insertSession(t, d, "a-old", "Old", "/repo", 1, 10)
	insertSession(t, d, "b-new", "New", "/repo", 1, 90)
	insertSession(t, d, "c-mid", "Mid", "/repo", 1, 50)
	entries, err := d.HistoryInventory(t.Context())
	if err != nil || len(entries) != 3 || entries[0].ID != "b-new" || entries[1].ID != "c-mid" || entries[2].ID != "a-old" {
		t.Fatalf("inventory order: %+v %v", entries, err)
	}
}

func TestHistoryIncludesHiddenSessionsAndFullPayloads(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	insertSession(t, d, "root", "Root", "/repo", 1, 3)
	insertSubagent(t, d, "child", "root", "completed subagent", "/repo", 1, 2)
	insertMessage(t, d, "message", "root", 1, map[string]any{"role": "user", "summary": map[string]any{"diffs": "retained patch"}})
	insertPart(t, d, "part", "message", "root", 1, map[string]any{"type": "text", "text": "hello"})
	entries, err := d.HistoryInventory(t.Context())
	if err != nil || len(entries) != 2 {
		t.Fatalf("complete inventory: %+v %v", entries, err)
	}
	snapshot, err := d.HistorySnapshot(t.Context(), "root")
	if err != nil || len(snapshot.Messages) != 1 || len(snapshot.Parts) != 1 || !strings.Contains(string(snapshot.Messages[0].Data), "retained patch") {
		t.Fatalf("full payload: %+v %v", snapshot, err)
	}
	if _, err := d.HistorySnapshot(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := d.HistorySourceKey(t.Context()); err == nil {
		t.Fatal("accepted memory database source identity")
	}
	path := filepath.Join(t.TempDir(), "source.db")
	if _, err := d.db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := reader.HistorySourceKey(t.Context()); err != nil || key != canonicalPath {
		t.Fatalf("source key %q %v", key, err)
	}
	if snapshot, err := reader.HistorySnapshot(t.Context(), "child"); err != nil || snapshot.Session.ParentID != "root" {
		t.Fatalf("child: %+v %v", snapshot, err)
	}
}

// OpenCode marks a subagent by its title convention, the rule analytics uses.
func TestHistorySnapshotMarksSubagentTitles(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	insertSession(t, d, "root", "Root", "/repo", 1, 3)
	insertSubagent(t, d, "child", "root", "Explore (@explore subagent)", "/repo", 1, 2)
	for id, want := range map[string]bool{"root": false, "child": true} {
		snapshot, err := d.HistorySnapshot(t.Context(), id)
		if err != nil || snapshot.Subagent != want {
			t.Fatalf("%s subagent = %+v, %v; want %v", id, snapshot, err, want)
		}
	}
}

func TestIsSubagentTitle(t *testing.T) {
	for title, want := range map[string]bool{
		"Explore (@explore subagent)": true,
		"Review (general SUBAGENT)":   true,
		"( subagent)":                 true,
		"Root":                        false,
		"completed subagent":          false,
		"subagent)":                   false,
		"Explore (@explore subagent":  false,
		"":                            false,
	} {
		if got := IsSubagentTitle(title); got != want {
			t.Errorf("IsSubagentTitle(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestV2HistoryRetainsNativeAndUnsupportedMessages(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	d, err := Open(writeV2DB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	snapshot, err := d.HistorySnapshot(t.Context(), "ses_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 || len(snapshot.NativeMessages) != 3 || snapshot.Messages[0].ID != "msg_0001" || !strings.Contains(string(snapshot.NativeMessages["msg_0001"]), "sys") {
		t.Fatalf("native history omitted system message: %+v", snapshot)
	}
	if len(snapshot.Parts) == 0 || snapshot.Parts[0].ID == "" {
		t.Fatal("shared converter parts missing")
	}
}

func TestHistoryStorageErrorsAndCancellation(t *testing.T) {
	d := openTestDB(t)
	insertSession(t, d, "root", "Root", "/repo", 1, 2)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.HistorySnapshot(ctx, "root"); err == nil {
		t.Fatal("read cancelled snapshot")
	}
	if _, err := d.HistoryInventory(ctx); err == nil {
		t.Fatal("read cancelled inventory")
	}
	if _, err := d.db.Exec("DROP TABLE part"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.HistorySnapshot(t.Context(), "root"); err == nil {
		t.Fatal("returned snapshot with missing parts table")
	}
	if _, err := d.db.Exec("DROP TABLE message"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.HistorySnapshot(t.Context(), "root"); err == nil {
		t.Fatal("returned snapshot with missing message table")
	}
	d.Close()
	if _, err := d.HistoryInventory(t.Context()); err == nil {
		t.Fatal("read closed inventory")
	}
	if _, err := d.HistorySourceKey(t.Context()); err == nil {
		t.Fatal("read closed source identity")
	}
}
