package platformconnections

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

func TestAdditiveSchemaPreservesLegacySettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"platforms.enabled": "claudecode,codex", "platform.default": "codex"} {
		if err := db.SetSetting(t.Context(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for key, want := range map[string]string{"platforms.enabled": "claudecode,codex", "platform.default": "codex"} {
		got, ok, err := db.GetSetting(t.Context(), key)
		if err != nil || !ok || got != want {
			t.Fatalf("legacy setting %s changed: %q %v %v", key, got, ok, err)
		}
	}
	got, err := New(db).Read(t.Context())
	if err != nil || got.Initialized {
		t.Fatalf("schema migration guessed installation state: %+v %v", got, err)
	}
}

func TestSchemaConstraints(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	for _, query := range []string{
		`UPDATE platform_connection SET kind='unknown'`,
		`UPDATE platform_connection SET name=''`,
		`UPDATE platform_connection SET configuration='unknown'`,
		`UPDATE platform_connection SET enabled=2`,
		`UPDATE platform_connection SET removed=2`,
		`UPDATE platform_connection SET removed=1`,
		`UPDATE platform_connection SET legacy_kind='opencode'`,
		`UPDATE platform_connection SET legacy_kind='claudecode'`,
		`UPDATE platform_connection_collection SET onboarding_complete=2`,
		`UPDATE platform_connection_collection SET default_id='missing'`,
		`UPDATE platform_connection_collection SET id=2`,
	} {
		if _, err := db.SQL().ExecContext(t.Context(), query); err == nil {
			t.Fatalf("constraint accepted %s", query)
		}
	}
	got, err := s.Get(t.Context(), c.ID)
	if err != nil || got != c {
		t.Fatalf("rejected writes changed identity: %+v %v", got, err)
	}
	if _, err := s.Initialize(t.Context(), Seed{}); err != nil {
		t.Fatal(err)
	}
}
