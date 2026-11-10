package state

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/configfile"
)

func TestFileConfigMigrationRetainsBackupAndUsesFile(t *testing.T) {
	db := openTestStateDB(t)
	ctx := t.Context()
	for key, value := range map[string]string{
		"judge_model":                     "old/model",
		"session.archive_resurface":       "activity",
		"initial_import_archive/opencode": "1",
		"webhook.enrollment_token":        "legacy-enrollment-secret",
		"judge_endpoint":                  `{"format":"openai","endpoint":"https://reviewer.example/v1/chat/completions","model":"m","apiKey":"legacy-reviewer-secret","minSafeProbability":0.99}`,
	} {
		if err := db.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetSecretSetting(ctx, "preview_apps", `[{"kind":"github","clientId":"id","clientSecret":"preview-secret"}]`); err != nil {
		t.Fatal(err)
	}
	if err := db.SetJudgeDelayMs(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPromptSections(ctx, []PromptSection{{Title: "rule", Content: "ask first"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := db.EnableFileConfig(ctx, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"legacy-enrollment-secret", "legacy-reviewer-secret", "preview-secret", "remote_token_secret", "config.encryption_key", "initial_import_archive"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("configuration leaked secret/runtime state %s", forbidden)
		}
	}
	if !strings.Contains(string(data), "secret://ocman/") {
		t.Fatal("migration did not create secret references")
	}
	if ms, err := db.GetJudgeDelayMs(ctx); err != nil || ms != 0 {
		t.Fatalf("delay = %d, %v", ms, err)
	}
	sections, err := db.GetPromptSections(ctx)
	if err != nil || len(sections) != 1 || sections[0].Title != "rule" {
		t.Fatalf("sections = %+v, %v", sections, err)
	}
	for key, secret := range map[string]string{"preview_apps": "preview-secret", "webhook.enrollment_token": "legacy-enrollment-secret", "judge_endpoint": "legacy-reviewer-secret"} {
		value, ok, err := db.GetSetting(ctx, key)
		if err != nil || !ok || !strings.Contains(value, secret) {
			t.Fatalf("migrated secret %s did not resolve: %v", key, err)
		}
	}
	if err := db.SetSetting(ctx, "judge_model", "new/model"); err != nil {
		t.Fatal(err)
	}
	var backup string
	if err := db.db.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = 'judge_model'`).Scan(&backup); err != nil || backup != "old/model" {
		t.Fatalf("backup changed: %q, %v", backup, err)
	}
	if err := db.EnableFileConfig(ctx, path); err != nil {
		t.Fatal(err)
	}
	value, _, err := db.GetSetting(ctx, "judge_model")
	if err != nil || value != "new/model" {
		t.Fatalf("restart reverted file: %q, %v", value, err)
	}
	f := &configfile.File{Path: path}
	if err := f.Update(func(doc *configfile.Document) error {
		doc.Delete("settings", "judge_model")
		doc.Delete("settings", "session.archive_resurface")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GetSetting(ctx, "judge_model"); err != nil || ok {
		t.Fatal("missing file setting fell back to old DB row")
	}
	flags, err := db.SidebarFlags(ctx)
	if err != nil || flags.Mode != "halt" {
		t.Fatalf("sidebar used stale DB setting: %s, %v", flags.Mode, err)
	}
	_, mode, err := db.SidebarArchives(ctx, nil)
	if err != nil || mode != "halt" {
		t.Fatalf("archive query used stale DB setting: %s, %v", mode, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableFileConfig(ctx, path); err == nil {
		t.Fatal("deleted config was regenerated from stale backup")
	}
}

func TestFileConfigExternalSecretWritesRefused(t *testing.T) {
	db := openTestStateDB(t)
	t.Setenv("CONFIG_SECRET_TEST", "external-secret")
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := "version: 1\nsettings:\n  webhook.enrollment_token: secret://env/CONFIG_SECRET_TEST\n  judge_endpoint:\n    endpoint: https://example.com\n    apiKey: secret://env/CONFIG_SECRET_TEST\n"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"replacement", "", "external-secret"} {
		if err := db.SetSetting(t.Context(), "webhook.enrollment_token", value); !errors.Is(err, ErrExternalSecret) {
			t.Fatalf("external replacement accepted: %v", err)
		}
	}
	if err := db.SetSetting(t.Context(), "judge_endpoint", `{"endpoint":"https://example.com","apiKey":"replacement"}`); !errors.Is(err, ErrExternalSecret) {
		t.Fatalf("external endpoint replacement accepted: %v", err)
	}
	// Non-secret endpoint changes retain the reference instead of copying its value.
	if err := db.SetSetting(t.Context(), "judge_endpoint", `{"endpoint":"https://example.com","model":"new","apiKey":"external-secret"}`); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "external-secret") || !strings.Contains(string(data), "secret://env/CONFIG_SECRET_TEST") {
		t.Fatal("external credential copied into config")
	}
	t.Setenv("CONFIG_SECRET_TEST", "")
	if _, _, err := db.GetSetting(t.Context(), "webhook.enrollment_token"); err == nil {
		t.Fatal("missing external secret silently fell back")
	}
	if err := db.EnableFileConfig(t.Context(), path); err == nil {
		t.Fatal("startup accepted missing required secret")
	}
}

func TestFileConfigManagedSecretsAndReadOnlyFailure(t *testing.T) {
	db := openTestStateDB(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := db.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first-secret", "replacement-secret", ""} {
		if err := db.SetSecretSetting(t.Context(), "preview_apps", value); err != nil {
			t.Fatal(err)
		}
		got, ok, err := db.GetSecretSetting(t.Context(), "preview_apps")
		if err != nil || !ok || got != value {
			t.Fatalf("managed secret mismatch: %v", err)
		}
		data, _ := os.ReadFile(path)
		if value != "" && strings.Contains(string(data), value) {
			t.Fatal("secret written as plaintext")
		}
	}
	if err := db.SetSetting(t.Context(), "webhook.enrollment_token", "preserved-secret"); err != nil {
		t.Fatal(err)
	}
	doc, err := (&configfile.File{Path: path}).Read()
	if err != nil {
		t.Fatal(err)
	}
	ref, _, _ := doc.Get("settings", "webhook.enrollment_token")
	parsed, _ := configfile.ParseSecret(ref)
	if value, err := ReadConfigSecret(t.Context(), filepath.Join(db.dataDir, "state.db"), parsed.Reference); err != nil || value != "preserved-secret" {
		t.Fatalf("cannot read managed slot through read-only startup resolver: %v", err)
	}
	// Cookie-key rotation must not change the configuration encryption key.
	if err := db.SetAuthSecret(t.Context(), []byte("new-cookie-signing-key")); err != nil {
		t.Fatal(err)
	}
	if value, _, err := db.GetSetting(t.Context(), "webhook.enrollment_token"); err != nil || value != "preserved-secret" {
		t.Fatalf("cookie rotation broke secrets: %v", err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(t.Context(), "webhook.enrollment_token", "lost-secret"); err == nil {
		t.Fatal("save to read-only config succeeded")
	}
	if value, _, err := db.GetSetting(t.Context(), "webhook.enrollment_token"); err != nil || value != "preserved-secret" {
		t.Fatalf("failed save replaced active credential: %v", err)
	}
}

func TestFileConfigProjectAndJudgeEdits(t *testing.T) {
	db := openTestStateDB(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := db.SetProjectSettings(t.Context(), "/repo", ProjectSettings{Models: []string{"p/m"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if err := db.SetProjectSettings(t.Context(), "/repo", ProjectSettings{}); err != nil {
		t.Fatal(err)
	}
	if value, err := db.GetProjectSettings(t.Context(), "/repo"); err != nil || len(value.Models) != 0 {
		t.Fatalf("removed models still visible: %+v, %v", value, err)
	}
	if err := db.SetJudgeDelayMs(t.Context(), 12); err != nil {
		t.Fatal(err)
	}
	if ms, err := db.GetJudgeDelayMs(t.Context()); err != nil || ms != 12 {
		t.Fatalf("delay update: %d, %v", ms, err)
	}
	if err := db.SetPromptSections(t.Context(), []PromptSection{{Title: "new", Content: "rule"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableManagedSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.SetJudgeDelayMs(t.Context(), 1); !errors.Is(err, ErrManagedSetting) {
		t.Fatalf("managed write allowed: %v", err)
	}
	if err := db.ApplyManagedSettings(t.Context(), ManagedSettings{Hub: "hub", JudgeModel: "hub/model", JudgeDelayMs: 3}, 1); err != nil {
		t.Fatal(err)
	}
	if ms, err := db.GetJudgeDelayMs(t.Context()); err != nil || ms != 3 {
		t.Fatalf("hub overlay lost: %d, %v", ms, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "hub/model") {
		t.Fatal("hub snapshot was written to local config")
	}
}

func TestFileConfigRejectsUnsafeDocuments(t *testing.T) {
	for _, settings := range []string{
		"  remote_token_secret: do-not-export\n",
		"  webhook.enrollment_token: plaintext\n",
		"  webhook.enrollment_token: secret://unknown/key\n",
		"  judge_endpoint: {apiKey: plaintext}\n",
	} {
		db := openTestStateDB(t)
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("version: 1\nsettings:\n"+settings), 0600); err != nil {
			t.Fatal(err)
		}
		if err := db.EnableFileConfig(t.Context(), path); err == nil {
			t.Fatalf("accepted unsafe settings: %s", settings)
		}
	}
}

func TestFileConfigReviewerManagedSecretMigration(t *testing.T) {
	db := openTestStateDB(t)
	if err := db.EnableFileConfig(t.Context(), filepath.Join(t.TempDir(), "config.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"first", "second", ""} {
		value, _ := json.Marshal(map[string]string{"apiKey": secret})
		if err := db.SetSetting(t.Context(), "judge_endpoint", string(value)); err != nil {
			t.Fatal(err)
		}
		actual, _, err := db.GetSetting(t.Context(), "judge_endpoint")
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]string
		if err := json.Unmarshal([]byte(actual), &fields); err != nil || fields["apiKey"] != secret {
			t.Fatalf("reviewer key mismatch: %v", err)
		}
	}
}

func TestFileConfigBatchSettingsOwnership(t *testing.T) {
	db := openTestStateDB(t)
	if err := db.SetSettings(t.Context(), map[string]string{"judge_model": "before/model", "session.default_agent": "build"}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableFileConfig(t.Context(), filepath.Join(t.TempDir(), "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSettings(t.Context(), map[string]string{"judge_model": "after/model", "session.default_agent": "plan"}); err != nil {
		t.Fatal(err)
	}
	if value, _, err := db.GetSetting(t.Context(), "judge_model"); err != nil || value != "after/model" {
		t.Fatalf("batch save = %q, %v", value, err)
	}
	if err := db.SetSettings(t.Context(), map[string]string{"judge_model": "lost/model", "runtime-only": "no"}); err == nil {
		t.Fatal("mixed config/runtime transaction accepted")
	}
	if err := db.EnableManagedSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSettings(t.Context(), map[string]string{"judge_model": "no/model"}); !errors.Is(err, ErrManagedSetting) {
		t.Fatal("batch bypassed managed remote ownership")
	}
}

func TestFileConfigRejectsCorruptCredentials(t *testing.T) {
	db := openTestStateDB(t)
	ctx := t.Context()
	ref, err := db.storeConfigSecret(ctx, "credential")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := configfile.ParseSecret(ref)
	for _, ciphertext := range []string{"not base64!", base64.RawStdEncoding.EncodeToString([]byte("short")), base64.RawStdEncoding.EncodeToString(make([]byte, 40))} {
		if err := db.setSetting(ctx, "config.secret/"+parsed.Reference, ciphertext); err != nil {
			t.Fatal(err)
		}
		if _, err := db.loadConfigSecret(ctx, parsed.Reference); err == nil {
			t.Fatal("corrupt credential was accepted")
		}
	}
	if _, err := db.loadConfigSecret(ctx, "absent"); err == nil {
		t.Fatal("missing credential was accepted")
	}
	for _, key := range []string{"bad base64!", base64.RawStdEncoding.EncodeToString([]byte("short"))} {
		if err := db.setSetting(ctx, "config.encryption_key", key); err != nil {
			t.Fatal(err)
		}
		if _, err := db.storeConfigSecret(ctx, "replacement"); err == nil {
			t.Fatal("corrupt encryption key was accepted")
		}
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM setting WHERE key = 'config.encryption_key'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.loadConfigSecret(ctx, parsed.Reference); err == nil {
		t.Fatal("read regenerated a missing encryption key")
	}
	if _, err := db.storeConfigSecret(ctx, "replacement"); err == nil {
		t.Fatal("write regenerated a missing encryption key")
	}
	if _, err := ReadConfigSecret(ctx, filepath.Join(t.TempDir(), "absent.db"), "key"); err == nil {
		t.Fatal("read-only loader created a database")
	}
}

func TestFileConfigValidationAndMissingFiles(t *testing.T) {
	for _, settings := range []string{"judge_delay: -1", "judge_delay: invalid", "judge_endpoint: []", "judge_endpoint: {apiKey: 12}"} {
		db := openTestStateDB(t)
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("version: 1\nsettings:\n  "+settings+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := db.EnableFileConfig(t.Context(), path); err == nil {
			t.Fatalf("accepted invalid setting: %s", settings)
		}
	}
	db := openTestStateDB(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := db.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	f := &configfile.File{Path: path}
	for _, enabled := range []bool{false, true} {
		if err := f.Update(func(d *configfile.Document) error { return d.Set("settings", "worktree.inherit_permissions", enabled) }); err != nil {
			t.Fatal(err)
		}
		if value, err := db.GetWorktreeInheritPermissions(t.Context()); err != nil || value != enabled {
			t.Fatalf("boolean inheritance = %v, %v", value, err)
		}
	}
	if err := os.WriteFile(path, []byte("version: 1\nsettings: {unknown: x}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetSetting(t.Context(), "judge_model"); err == nil {
		t.Fatal("runtime read ignored malformed configuration")
	}
	if err := db.SetSetting(t.Context(), "judge_model", "p/m"); err == nil {
		t.Fatal("runtime write ignored malformed configuration")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetSetting(t.Context(), "judge_model"); err == nil {
		t.Fatal("runtime read ignored missing configuration")
	}
}

func TestFileConfigLegacyCredentialErrorsLeaveSourceIntact(t *testing.T) {
	for _, value := range []string{"not base64!", base64.RawStdEncoding.EncodeToString([]byte("truncated"))} {
		db := openTestStateDB(t)
		if err := db.SetSetting(t.Context(), "preview_apps", value); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := db.EnableFileConfig(t.Context(), path); err == nil {
			t.Fatal("migrated unreadable legacy credential")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("published incomplete migration")
		}
		if got, _, err := db.GetSetting(t.Context(), "preview_apps"); err != nil || got != value {
			t.Fatal("failed migration changed backup")
		}
	}
}
