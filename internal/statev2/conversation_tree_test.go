package statev2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestConversationTreePreservesNativeHierarchyAndRetainedSources(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	for _, tc := range []struct {
		instance, id, parent, ext string
		deleted                   bool
	}{
		{"first", "root", "", `{}`, false},
		{"first", "child", "root", `{}`, false},
		{"first", "grandchild", "child", `{"SourceChild":true}`, false},
		{"first", "retained", "root", `{}`, true},
		{"first", "worktree", "root", `{"SourceChild":false}`, false},
		{"first", "worktree-child", "worktree", `{}`, false},
		{"first", "cycle-a", "cycle-b", `{}`, false},
		{"first", "cycle-b", "cycle-a", `{}`, false},
		{"first", "orphan", "missing", `{}`, false},
		{"second", "root", "grandchild", `{}`, false},
		{"second", "foreign-child", "root", `{}`, false},
	} {
		b := summaryBatch(tc.instance, tc.id, tc.parent)
		b.Session.Extensions, b.Session.SourceDeleted = json.RawMessage(tc.ext), tc.deleted
		commit(t, d, b)
	}
	for _, tc := range []struct {
		id   string
		want []string
	}{
		{"root", []string{"child", "grandchild", "retained", "root"}},
		{"grandchild", []string{"child", "grandchild", "retained", "root"}},
		{"worktree", []string{"worktree", "worktree-child"}},
		{"cycle-a", []string{"cycle-a", "cycle-b"}},
		{"orphan", []string{"orphan"}},
	} {
		page, err := d.ReadConversation(t.Context(), Key{"first", tc.id}, 30, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(page.Tree))
		for i, row := range page.Tree {
			ids[i] = row.SessionID
			if row.InstanceID != "first" {
				t.Fatal("source crossed")
			}
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.id, ids, tc.want)
		}
	}
}
