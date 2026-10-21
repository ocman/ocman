package statev2

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestStoredProjectsScopeTotalsAndDirectoryEvidence(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	if err := d.RegisterInstance(t.Context(), Instance{ID: "foreign", OwnerID: "other", Platform: "test", SourceKey: "foreign"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		instance, id, dir, root string
		helper, deleted         bool
	}{
		{"first", "root", "/repo/worktree", "/repo", false, false},
		{"first", "helper", "/repo/worktree", "/repo", true, false},
		{"second", "root", "/repo/worktree", "/repo", false, false},
		{"foreign", "root", "/foreign", "/foreign", false, false},
		{"first", "deleted", "/deleted", "/deleted", false, true},
		{"first", "empty", "", "", false, false},
	} {
		commit(t, d, Batch{Session: Session{Key: Key{InstanceID: row.instance, SessionID: row.id}, Directory: row.dir, ProjectID: "project", Subagent: row.helper, SourceDeleted: row.deleted, UpdatedAt: 20, Extensions: json.RawMessage(`{"ProjectDirectory":"` + row.root + `"}`)}, Messages: []Message{{ID: "m", Role: "assistant", Extensions: json.RawMessage(`{"role":"assistant","tokens":{"input":7,"output":3},"cost":0.5}`)}}})
	}
	projects, err := d.Projects(t.Context(), "owner")
	if err != nil || len(projects) != 1 {
		t.Fatalf("%+v %v", projects, err)
	}
	if got := projects[0]; got.Directory != "/repo/worktree" || got.SessionCount != 2 || got.MessageCount != 3 || got.TotalTokensIn != 21 || got.TotalTokensOut != 9 || got.TotalCost != 1.5 || got.LastUsed != 20 {
		t.Fatal(got)
	}
	if dirs, err := d.Directories(t.Context(), "owner"); err != nil || !reflect.DeepEqual(dirs, []string{"/repo/worktree"}) {
		t.Fatalf("%v %v", dirs, err)
	}
	if root, err := d.ProjectDirectory(t.Context(), "owner", "project"); err != nil || root != "/repo" {
		t.Fatalf("%q %v", root, err)
	}
	commit(t, d, Batch{Session: Session{Key: Key{InstanceID: "second", SessionID: "ambiguous"}, ProjectID: "project", Extensions: json.RawMessage(`{"ProjectDirectory":"/elsewhere"}`)}})
	if _, err := d.ProjectDirectory(t.Context(), "owner", "project"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous project accepted: %v", err)
	}
}

func TestStoredProjectQueriesValidateScopeAndFailClosed(t *testing.T) {
	d := openTestDB(t)
	if version, err := d.ProjectVersion(t.Context(), "owner"); err != nil || version != 0 {
		t.Fatalf("empty version: %d %v", version, err)
	}
	if _, err := d.ProjectVersion(t.Context(), ""); err == nil {
		t.Fatal("empty version owner accepted")
	}
	if _, err := d.Projects(t.Context(), ""); err == nil {
		t.Fatal("empty owner accepted")
	}
	if _, err := d.Directories(t.Context(), ""); err == nil {
		t.Fatal("empty owner accepted")
	}
	if _, err := d.ProjectDirectory(t.Context(), "", "p"); err == nil {
		t.Fatal("empty owner accepted")
	}
	for _, project := range []string{"", "missing"} {
		if root, err := d.ProjectDirectory(t.Context(), "owner", project); err != nil || root != "" {
			t.Fatalf("%q %v", root, err)
		}
	}
	for n, ext := range []string{`{}`, `{"ProjectDirectory":"/"}`, `{"ProjectDirectory":1}`, `{"ProjectDirectory":[]}`} {
		commit(t, d, Batch{Session: Session{Key: Key{InstanceID: "first", SessionID: string(rune('a' + n))}, ProjectID: "unattributed", Extensions: json.RawMessage(ext)}})
	}
	if root, err := d.ProjectDirectory(t.Context(), "owner", "unattributed"); err != nil || root != "" {
		t.Fatalf("invalid evidence accepted: %q %v", root, err)
	}
	if version, err := d.ProjectVersion(t.Context(), "owner"); err != nil || version != 4 {
		t.Fatalf("committed version: %d %v", version, err)
	}
	if version, err := d.ProjectVersion(t.Context(), "other"); err != nil || version != 0 {
		t.Fatalf("owner leak: %d %v", version, err)
	}
	_ = d.Close()
	if _, err := d.ProjectVersion(t.Context(), "owner"); err == nil {
		t.Fatal("closed version DB accepted")
	}
	if _, err := d.Projects(t.Context(), "owner"); err == nil {
		t.Fatal("closed DB accepted")
	}
	if _, err := d.Directories(t.Context(), "owner"); err == nil {
		t.Fatal("closed DB accepted")
	}
	if _, err := d.ProjectDirectory(t.Context(), "owner", "p"); err == nil {
		t.Fatal("closed DB accepted")
	}
}
