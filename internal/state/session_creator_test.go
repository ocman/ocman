package state

import (
	"path/filepath"
	"testing"
)

func TestSessionCreatorPersistenceAndScopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	creator := SessionCreator{Platform: "r-machine:claude", SessionID: "child", CreatorPlatform: "opencode", CreatorSessionID: "parent", CreatedAt: 42}
	if err := d.RecordSessionCreator(t.Context(), creator); err != nil {
		t.Fatal(err)
	}
	repeated := creator
	repeated.CreatedAt = 99
	if err := d.RecordSessionCreator(t.Context(), repeated); err != nil {
		t.Fatal(err)
	}
	other := creator
	other.Platform = "claude"
	other.CreatorSessionID = "another-parent"
	other.CreatedAt = 0
	if err := d.RecordSessionCreator(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	got, err := d.GetSessionCreator(t.Context(), creator.Platform, creator.SessionID)
	if err != nil || got == nil || *got != creator {
		t.Fatalf("creator after reopen = %#v, %v", got, err)
	}
	got, err = d.GetSessionCreator(t.Context(), other.Platform, other.SessionID)
	if err != nil || got == nil || got.CreatorSessionID != other.CreatorSessionID || got.CreatedAt <= 0 {
		t.Fatalf("other owner = %#v, %v", got, err)
	}
	got, err = d.GetSessionCreator(t.Context(), "opencode", "unlinked")
	if err != nil || got != nil {
		t.Fatalf("unlinked = %#v, %v", got, err)
	}
	other.Platform = creator.Platform
	if err := d.RecordSessionCreator(t.Context(), other); err == nil {
		t.Fatal("overwrote creator")
	}
}

func TestSessionCreatorRejectsInvalidReferences(t *testing.T) {
	d := openTestStateDB(t)
	for _, row := range []SessionCreator{
		{SessionID: "child", CreatorPlatform: "opencode", CreatorSessionID: "parent"},
		{Platform: "opencode", CreatorPlatform: "opencode", CreatorSessionID: "parent"},
		{Platform: "opencode", SessionID: "child", CreatorSessionID: "parent"},
		{Platform: "opencode", SessionID: "child", CreatorPlatform: "opencode"},
		{Platform: "opencode", SessionID: "self", CreatorPlatform: "opencode", CreatorSessionID: "self"},
	} {
		if err := d.RecordSessionCreator(t.Context(), row); err == nil {
			t.Fatalf("accepted invalid reference %#v", row)
		}
	}
}

func TestSessionCreatorReferencesRekeyBothOwners(t *testing.T) {
	d := openTestStateDB(t)
	row := SessionCreator{Platform: "claude", SessionID: "old-child", CreatorPlatform: "opencode", CreatorSessionID: "old-parent"}
	if err := d.RecordSessionCreator(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	for platform, mapping := range map[string]map[string]string{
		"claude": {"old-child": "ocm_child"}, "opencode": {"old-parent": "ocm_parent"},
	} {
		refs, err := d.SessionReferences(t.Context(), platform)
		if err != nil || len(refs) != 1 {
			t.Fatalf("references = %v, %v", refs, err)
		}
		if err := d.RekeySessionReferences(t.Context(), platform, mapping); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.GetSessionCreator(t.Context(), "claude", "ocm_child")
	if err != nil || got == nil || got.CreatorSessionID != "ocm_parent" {
		t.Fatalf("rekeyed creator = %#v, %v", got, err)
	}
}

func TestMigrateSessionCreatorFromV130(t *testing.T) {
	d := openTestStateDB(t)
	if _, err := d.db.Exec(`DROP TABLE session_creator; DELETE FROM schema_version WHERE version > 130`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrate(d.db); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.RecordSessionCreator(t.Context(), SessionCreator{Platform: "opencode", SessionID: "child", CreatorPlatform: "opencode", CreatorSessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCreatorReadFailure(t *testing.T) {
	d := openTestStateDB(t)
	if _, err := d.db.Exec(`DROP TABLE session_creator`); err != nil {
		t.Fatal(err)
	}
	if got, err := d.GetSessionCreator(t.Context(), "opencode", "child"); err == nil || got != nil {
		t.Fatalf("read failure = %#v, %v", got, err)
	}
}
