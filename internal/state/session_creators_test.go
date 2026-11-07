package state

import (
	"path/filepath"
	"testing"
)

func TestSessionCreatorsBatchScope(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for _, c := range []SessionCreator{
		{Platform: "opencode", SessionID: "child", CreatorPlatform: "claude", CreatorSessionID: "parent", CreatedAt: 1},
		{Platform: "r-machine:opencode", SessionID: "child", CreatorPlatform: "opencode", CreatorSessionID: "remote-launcher", CreatedAt: 2},
	} {
		if err := d.RecordSessionCreator(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	keys := []Key{{Platform: "opencode", SessionID: "child"}, {Platform: "claude", SessionID: "child"}, {Platform: "opencode", SessionID: "unlinked"}}
	got, err := d.SessionCreators(t.Context(), keys)
	if err != nil || len(got) != 1 || got[keys[0]].CreatorSessionID != "parent" {
		t.Fatalf("scoped creators = %#v, %v", got, err)
	}
	remote := Key{Platform: "r-machine:opencode", SessionID: "child"}
	got, err = d.SessionCreators(t.Context(), []Key{remote})
	if err != nil || got[remote].CreatorSessionID != "remote-launcher" || got[remote].CreatedAt != 2 {
		t.Fatalf("remote creator = %#v, %v", got, err)
	}
	got, err = d.SessionCreators(t.Context(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty = %#v, %v", got, err)
	}
	if _, err := d.SQL().Exec(`DROP TABLE session_creator`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SessionCreators(t.Context(), keys); err == nil {
		t.Fatal("lost query error")
	}
}
