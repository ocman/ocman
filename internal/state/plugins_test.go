package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

func pluginFixture(t *testing.T) (*DB, plugins.Description, []PluginInstance) {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	desc := plugins.Description{ID: "io.ocman.test", Name: "Test", Version: "1.0", Protocol: plugins.Version{Major: 1}, MaxConcurrency: 1, Scope: plugins.ScopeOwner, Capabilities: []plugins.Capability{{Name: "action", Version: plugins.Version{Major: 1}}}}
	instances := []PluginInstance{{ID: "primary", Capability: desc.Capabilities[0], Scope: desc.Scope}}
	if err := d.DiscoverPlugin(t.Context(), desc, "/bin/plugin", strings.Repeat("a", 64), instances); err != nil {
		t.Fatal(err)
	}
	return d, desc, instances
}

func requirePluginOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPluginConfigurationRecovery(t *testing.T) {
	for _, tt := range []struct {
		name                        string
		enabled, working, candidate bool
	}{
		{"pending", true, true, true},
		{"disabled edits", false, true, true},
		{"never ready", true, false, true},
		{"unchanged unhealthy", true, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, desc, _ := pluginFixture(t)
			ctx, id := t.Context(), desc.ID
			requirePluginOK(t, d.SetPluginConfiguration(ctx, id, nil, map[string]string{"token": "working"}))
			if tt.working {
				requirePluginOK(t, d.MarkPluginConfigurationWorking(ctx, id))
			}
			if tt.candidate {
				// Secret-only edits must be recovered even when public values match.
				requirePluginOK(t, d.SetPluginConfiguration(ctx, id, nil, map[string]string{"token": "candidate"}))
			}
			requirePluginOK(t, d.SetPluginEnabled(ctx, id, tt.enabled, nil))
			requirePluginOK(t, d.SetPluginHealth(ctx, id, PluginHealth{Status: "unhealthy", RestartCount: 5}))
			requirePluginOK(t, d.RecoverPluginConfigurations(ctx))
			requirePluginOK(t, d.RecoverPluginConfigurations(ctx))
			recovered := tt.enabled && tt.working && tt.candidate
			want := "working"
			if tt.candidate && !recovered {
				want = "candidate"
			}
			requirePluginOK(t, d.WithPluginSecrets(ctx, id, func(secrets map[string]string) error {
				if secrets["token"] != want {
					t.Fatalf("secret = %q, want %q", secrets["token"], want)
				}
				return nil
			}))
			p, err := d.GetPlugin(ctx, id)
			requirePluginOK(t, err)
			if recovered && p.Health.Status != "starting" || !recovered && p.Health.RestartCount != 5 {
				t.Fatalf("unexpected recovery health: %+v", p.Health)
			}
			requirePluginOK(t, d.Close())
			if !errors.Is(d.RecoverPluginConfigurations(ctx), ErrPluginState) {
				t.Fatal("closed database recovery did not fail")
			}
		})
	}
}

func TestPluginRollbackResetsFailedCandidateHealth(t *testing.T) {
	d, desc, _ := pluginFixture(t)
	ctx, id := t.Context(), desc.ID
	requirePluginOK(t, d.SetPluginConfiguration(ctx, id, nil, map[string]string{"token": "working"}))
	requirePluginOK(t, d.MarkPluginConfigurationWorking(ctx, id))
	requirePluginOK(t, d.SetPluginEnabled(ctx, id, true, nil))
	requirePluginOK(t, d.SetPluginConfiguration(ctx, id, nil, map[string]string{"token": "candidate"}))
	requirePluginOK(t, d.SetPluginHealth(ctx, id, PluginHealth{Status: "unhealthy", RestartCount: 5}))
	requirePluginOK(t, d.RollbackPluginConfiguration(ctx, id))
	// Host exits after restoring snapshots, before relaunching the working process.
	requirePluginOK(t, d.RecoverPluginConfigurations(ctx))
	p, err := d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if p.Health.Status != "starting" || p.Health.RestartCount != 0 {
		t.Fatalf("failed candidate prevents working configuration restart: %+v", p.Health)
	}
}

func TestPluginLifecycle(t *testing.T) {
	d, desc, instances := pluginFixture(t)
	ctx, id := t.Context(), desc.ID
	p, err := d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if p.Enabled || p.Removed || p.LastWorkingConfiguration != nil || !reflect.DeepEqual(p.Description, desc) || !reflect.DeepEqual(p.Instances, instances) {
		t.Fatalf("initial registration: %+v", p)
	}
	requirePluginOK(t, d.SetPluginConfiguration(ctx, id, map[string]json.RawMessage{"region": json.RawMessage(`"eu"`)}, map[string]string{"token": "private-token"}))
	requirePluginOK(t, d.MarkPluginConfigurationWorking(ctx, id))
	requirePluginOK(t, d.SetPluginEnabled(ctx, id, true, []string{"project.read"}))
	requirePluginOK(t, d.DiscoverPlugin(ctx, desc, "/bin/plugin", strings.Repeat("a", 64), instances))
	p, err = d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if !p.Enabled || len(p.Grants) != 1 {
		t.Fatal("identical discovery lost approval")
	}
	path, err := d.PluginDataDir(ctx, id)
	requirePluginOK(t, err)
	if !strings.HasPrefix(path, d.dataDir+string(os.PathSeparator)) {
		t.Fatal("data outside state directory")
	}
	requirePluginOK(t, os.WriteFile(filepath.Join(path, "private"), []byte("data"), 0o600))
	requirePluginOK(t, d.SetPluginEnabled(ctx, id, false, nil))
	requirePluginOK(t, d.RemovePlugin(ctx, id))
	p, err = d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if !p.Removed || p.Enabled || !p.Configuration.Secrets["token"] || p.LastWorkingConfiguration == nil {
		t.Fatal("remove lost configuration")
	}
	if _, err := os.Stat(filepath.Join(path, "private")); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPluginEnabled(ctx, id, true, nil); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("enabled removed plugin: %v", err)
	}
	requirePluginOK(t, d.DiscoverPlugin(ctx, desc, "/bin/plugin", strings.Repeat("a", 64), instances))
	p, err = d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if p.Enabled || p.Removed || len(p.Grants) != 0 || !p.Configuration.Secrets["token"] {
		t.Fatal("rediscovery did not retain config and revoke approval")
	}
	requirePluginOK(t, d.SetPluginEnabled(ctx, id, true, []string{"project.read"}))
	if err := d.DeletePluginPermanently(ctx, id); !errors.Is(err, ErrPluginInvalid) {
		t.Fatalf("deleted enabled plugin: %v", err)
	}
	requirePluginOK(t, d.SetPluginGrants(ctx, id, nil))
	p, err = d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if len(p.Grants) != 0 {
		t.Fatal("grants not revoked")
	}
	requirePluginOK(t, d.SetPluginEnabled(ctx, id, false, nil))
	requirePluginOK(t, d.DeletePluginPermanently(ctx, id))
	requirePluginOK(t, d.DeletePluginPermanently(ctx, id))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("data retained after explicit deletion: %v", err)
	}
	if _, err := d.GetPlugin(ctx, id); !errors.Is(err, ErrPluginNotFound) {
		t.Fatal(err)
	}
	list, err := d.ListPlugins(ctx)
	requirePluginOK(t, err)
	if len(list) != 0 {
		t.Fatal(list)
	}
}

func TestPluginChangedDiscoveryRevokesApproval(t *testing.T) {
	for _, change := range []string{"checksum", "path", "description", "instances"} {
		t.Run(change, func(t *testing.T) {
			d, desc, instances := pluginFixture(t)
			requirePluginOK(t, d.SetPluginEnabled(t.Context(), desc.ID, true, []string{"read"}))
			path, checksum := "/bin/plugin", strings.Repeat("a", 64)
			switch change {
			case "checksum":
				checksum = strings.Repeat("b", 64)
			case "path":
				path = "/bin/other"
			case "description":
				desc.Version = "2.0"
			case "instances":
				instances[0].ID = "new"
			}
			requirePluginOK(t, d.DiscoverPlugin(t.Context(), desc, path, checksum, instances))
			p, err := d.GetPlugin(t.Context(), desc.ID)
			requirePluginOK(t, err)
			if p.Enabled || len(p.Grants) != 0 {
				t.Fatal("changed binary retained approval")
			}
		})
	}
}

func TestPluginConfigurationRollbackAndSecrets(t *testing.T) {
	d, desc, _ := pluginFixture(t)
	ctx, id := t.Context(), desc.ID
	if err := d.RollbackPluginConfiguration(ctx, id); !errors.Is(err, ErrPluginNotFound) {
		t.Fatal(err)
	}
	secret := "old-secret+\"\nvalue"
	values := map[string]json.RawMessage{"count": json.RawMessage(`1`)}
	requirePluginOK(t, d.SetPluginConfiguration(ctx, id, values, map[string]string{"token": secret}))
	requirePluginOK(t, d.MarkPluginConfigurationWorking(ctx, id))
	requirePluginOK(t, d.SetPluginConfiguration(ctx, id, map[string]json.RawMessage{"count": json.RawMessage(`2`)}, map[string]string{"token": "new-secret"}))
	requirePluginOK(t, d.RollbackPluginConfiguration(ctx, id))
	p, err := d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if string(p.Configuration.Values["count"]) != "1" || !p.Configuration.Secrets["token"] || !reflect.DeepEqual(p.Configuration, *p.LastWorkingConfiguration) {
		t.Fatalf("rollback: %+v", p)
	}
	requirePluginOK(t, d.WithPluginSecrets(ctx, id, func(values map[string]string) error {
		if values["token"] != secret {
			t.Fatal("secret rollback failed")
		}
		return nil
	}))
	if err := d.WithPluginSecrets(ctx, id, func(map[string]string) error { return fmt.Errorf("credential %s", secret) }); !errors.Is(err, ErrPluginState) {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(secret)
	message := secret + " " + string(encoded[1:len(encoded)-1]) + " " + url.QueryEscape(secret) + " new-secret"
	redacted := d.RedactPluginDiagnostics(id, message)
	if redacted != "[REDACTED] [REDACTED] [REDACTED] [REDACTED]" {
		t.Fatalf("redaction: %q", redacted)
	}
	requirePluginOK(t, d.SetPluginHealth(ctx, id, PluginHealth{Status: "unhealthy", RestartCount: 2, LastError: message}))
	p, err = d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if p.Health.LastError != redacted || p.Health.RestartCount != 2 {
		t.Fatal(p.Health)
	}
	serialized, _ := json.Marshal(p)
	if strings.Contains(string(serialized), "old-secret") || strings.Contains(string(serialized), "new-secret") || strings.Contains(fmt.Sprintf("%+v", p), "old-secret") {
		t.Fatal("read exposes secrets")
	}
	requirePluginOK(t, d.SetPluginConfiguration(ctx, id, nil, nil))
	requirePluginOK(t, d.WithPluginSecrets(ctx, id, func(values map[string]string) error {
		if values["token"] != secret {
			t.Fatal("omitted secret lost")
		}
		return nil
	}))
	requirePluginOK(t, d.SetPluginConfiguration(ctx, id, nil, map[string]string{"token": ""}))
	p, err = d.GetPlugin(ctx, id)
	requirePluginOK(t, err)
	if p.Configuration.Secrets["token"] {
		t.Fatal("secret not cleared")
	}
	// Neither current nor previous secret values enter the database or WAL.
	for _, name := range []string{"state.db", "state.db-wal"} {
		data, err := os.ReadFile(filepath.Join(d.dataDir, name))
		if name != "state.db" && os.IsNotExist(err) {
			continue
		}
		requirePluginOK(t, err)
		if strings.Contains(string(data), "old-secret") || strings.Contains(string(data), "new-secret") {
			t.Fatal("SQL persisted a secret")
		}
	}
	// Reopen proves that snapshot pointers and redaction history are durable.
	path := filepath.Join(d.dataDir, "state.db")
	requirePluginOK(t, d.Close())
	d, err = Open(path)
	requirePluginOK(t, err)
	defer d.Close()
	requirePluginOK(t, d.RollbackPluginConfiguration(ctx, id))
	requirePluginOK(t, d.WithPluginSecrets(ctx, id, func(values map[string]string) error {
		if values["token"] != secret {
			t.Fatal("reopened rollback lost secret")
		}
		return nil
	}))
	list, err := d.ListPlugins(ctx)
	requirePluginOK(t, err)
	if len(list) != 1 || !list[0].Configuration.Secrets["token"] {
		t.Fatal(list)
	}
}

func TestPluginConfigurationSQLFailure(t *testing.T) {
	d, desc, _ := pluginFixture(t)
	requirePluginOK(t, d.SetPluginConfiguration(t.Context(), desc.ID, nil, map[string]string{"key": "working"}))
	root, err := d.pluginDirectory(desc.ID, "plugin-secrets")
	requirePluginOK(t, err)
	defer root.Close()
	before, err := os.ReadDir(root.Name())
	requirePluginOK(t, err)
	_, err = d.db.Exec(`CREATE TRIGGER reject_plugin_config BEFORE UPDATE OF config_json ON plugin_registration BEGIN SELECT RAISE(ABORT,'private failure'); END`)
	requirePluginOK(t, err)
	if err := d.SetPluginConfiguration(t.Context(), desc.ID, nil, map[string]string{"key": "candidate"}); !errors.Is(err, ErrPluginState) {
		t.Fatal(err)
	}
	after, err := os.ReadDir(root.Name())
	requirePluginOK(t, err)
	if len(before) != len(after) {
		t.Fatal("failed update leaked a secret file")
	}
	requirePluginOK(t, d.WithPluginSecrets(t.Context(), desc.ID, func(s map[string]string) error {
		if s["key"] != "working" {
			t.Fatal("failed update changed active secret")
		}
		return nil
	}))
}

func TestPluginFileModesAndUnsafePaths(t *testing.T) {
	d, desc, _ := pluginFixture(t)
	requirePluginOK(t, d.SetPluginConfiguration(t.Context(), desc.ID, nil, map[string]string{"key": "sensitive"}))
	root, err := d.pluginDirectory(desc.ID, "plugin-secrets")
	requirePluginOK(t, err)
	defer root.Close()
	entries, err := os.ReadDir(root.Name())
	requirePluginOK(t, err)
	for _, path := range []string{d.dataDir, filepath.Dir(root.Name()), root.Name()} {
		info, err := os.Stat(path)
		requirePluginOK(t, err)
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("directory mode: %v", info.Mode())
		}
	}
	file := filepath.Join(root.Name(), entries[0].Name())
	info, err := os.Stat(file)
	requirePluginOK(t, err)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode %v", info.Mode())
	}
	requirePluginOK(t, os.Chmod(file, 0o644))
	if _, err := d.GetPlugin(t.Context(), desc.ID); !errors.Is(err, ErrPluginState) {
		t.Fatalf("read insecure secret: %v", err)
	}
	if got := d.RedactPluginDiagnostics(desc.ID, "sensitive"); got != "[diagnostics unavailable]" {
		t.Fatal(got)
	}
	requirePluginOK(t, os.Chmod(file, 0o600))
	requirePluginOK(t, os.WriteFile(file, []byte(`{"key": "sensitive"`), 0o600))
	if _, err := d.GetPlugin(t.Context(), desc.ID); !errors.Is(err, ErrPluginState) {
		t.Fatalf("malformed secret: %v", err)
	}
	requirePluginOK(t, os.Remove(file))
	requirePluginOK(t, os.Symlink(filepath.Join(t.TempDir(), "outside"), file))
	if _, err := d.GetPlugin(t.Context(), desc.ID); !errors.Is(err, ErrPluginState) {
		t.Fatalf("read symlink: %v", err)
	}
	if _, err := d.readPluginSecrets(desc.ID, "../outside.json"); !errors.Is(err, ErrPluginState) {
		t.Fatal(err)
	}
	requirePluginOK(t, os.Symlink(t.TempDir(), filepath.Join(d.dataDir, "plugin-data")))
	if _, err := d.PluginDataDir(t.Context(), desc.ID); !errors.Is(err, ErrPluginState) {
		t.Fatalf("followed directory symlink: %v", err)
	}
}

func TestPluginUpgradeFromMain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	requirePluginOK(t, err)
	t.Cleanup(func() { _ = db.Close() })
	requirePluginOK(t, ensureSchemaVersionTable(db))
	tx, err := db.Begin()
	requirePluginOK(t, err)
	defer func() { _ = tx.Rollback() }()
	for version := 1; version <= 90; version++ {
		requirePluginOK(t, applyMigration(tx, version))
	}
	_, err = tx.Exec(`INSERT INTO schema_version VALUES(90,0); INSERT INTO projects_cache VALUES(1,'[]',123)`)
	requirePluginOK(t, err)
	requirePluginOK(t, tx.Commit())
	requirePluginOK(t, db.Close())
	d, err := Open(path)
	requirePluginOK(t, err)
	defer d.Close()
	version, err := currentSchemaVersion(d.db)
	requirePluginOK(t, err)
	if version != latestSchemaVersion {
		t.Fatalf("schema = %d, want %d", version, latestSchemaVersion)
	}
	var projects string
	var refreshed int
	requirePluginOK(t, d.db.QueryRow(`SELECT projects_json,refreshed_at FROM projects_cache WHERE id=1`).Scan(&projects, &refreshed))
	if projects != "[]" || refreshed != 123 {
		t.Fatal("main project cache changed during upgrade")
	}
	registrations, err := d.ListPlugins(t.Context())
	requirePluginOK(t, err)
	if len(registrations) != 0 {
		t.Fatal("upgrade created plugin registrations")
	}
	requirePluginOK(t, d.ReservePluginOperation(t.Context(), "org.example.test", "operation"))
}

func TestPluginMigration(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprint(rollback), func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			requirePluginOK(t, err)
			defer db.Close()
			_, err = db.Exec(`CREATE TABLE schema_version(version INTEGER PRIMARY KEY,applied_at INTEGER NOT NULL); INSERT INTO schema_version VALUES(88,0); CREATE TABLE retained(value TEXT); INSERT INTO retained VALUES('keep')`)
			requirePluginOK(t, err)
			if rollback {
				_, err = db.Exec(`CREATE TRIGGER reject_version BEFORE INSERT ON schema_version BEGIN SELECT RAISE(ABORT,'no'); END`)
				requirePluginOK(t, err)
			}
			err = migrate(db)
			if rollback {
				if err == nil {
					t.Fatal("expected migration rollback")
				}
			} else {
				requirePluginOK(t, err)
				requirePluginOK(t, migrate(db))
			}
			version, err := currentSchemaVersion(db)
			requirePluginOK(t, err)
			want := latestSchemaVersion
			if rollback {
				want = 88
			}
			if version != want {
				t.Fatal(version)
			}
			var count int
			requirePluginOK(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='plugin_registration'`).Scan(&count))
			if (count == 0) != rollback {
				t.Fatal("migration not atomic")
			}
			var value string
			requirePluginOK(t, db.QueryRow(`SELECT value FROM retained`).Scan(&value))
			if value != "keep" {
				t.Fatal(value)
			}
		})
	}
}

func TestPluginValidationAndMissing(t *testing.T) {
	d, desc, instances := pluginFixture(t)
	ctx := t.Context()
	for _, mutate := range []func(*plugins.Description, *string, *string, *[]PluginInstance){
		func(d *plugins.Description, _ *string, _ *string, _ *[]PluginInstance) { d.ID = "../bad" },
		func(_ *plugins.Description, p *string, _ *string, _ *[]PluginInstance) { *p = "relative" },
		func(_ *plugins.Description, _ *string, c *string, _ *[]PluginInstance) { *c = "invalid" },
		func(_ *plugins.Description, _ *string, _ *string, i *[]PluginInstance) { *i = append(*i, (*i)[0]) },
		func(_ *plugins.Description, _ *string, _ *string, i *[]PluginInstance) {
			(*i)[0].Capability.Name = "unknown"
		},
		func(_ *plugins.Description, _ *string, _ *string, i *[]PluginInstance) {
			(*i)[0].Scope = plugins.ScopeHub
		},
	} {
		copy := desc
		path, checksum := "/bin/plugin", strings.Repeat("a", 64)
		caps := append([]PluginInstance{}, instances...)
		mutate(&copy, &path, &checksum, &caps)
		if err := d.DiscoverPlugin(ctx, copy, path, checksum, caps); !errors.Is(err, ErrPluginInvalid) {
			t.Fatal(err)
		}
	}
	for _, grants := range [][]string{{"bad space"}, {"duplicate", "duplicate"}} {
		if err := d.SetPluginEnabled(ctx, desc.ID, true, grants); !errors.Is(err, ErrPluginInvalid) {
			t.Fatal(err)
		}
		if err := d.SetPluginGrants(ctx, desc.ID, grants); !errors.Is(err, ErrPluginInvalid) {
			t.Fatal(err)
		}
	}
	for _, health := range []PluginHealth{{Status: "secret"}, {Status: "ready", RestartCount: -1}} {
		if err := d.SetPluginHealth(ctx, desc.ID, health); !errors.Is(err, ErrPluginInvalid) {
			t.Fatal(err)
		}
	}
	for _, values := range []map[string]json.RawMessage{{"bad key": json.RawMessage(`1`)}, {"key": json.RawMessage(`!`)}} {
		if err := d.SetPluginConfiguration(ctx, desc.ID, values, nil); !errors.Is(err, ErrPluginInvalid) {
			t.Fatal(err)
		}
	}
	if err := d.SetPluginConfiguration(ctx, desc.ID, nil, map[string]string{"bad key": "value"}); !errors.Is(err, ErrPluginInvalid) {
		t.Fatal(err)
	}
	if err := d.SetPluginConfiguration(ctx, desc.ID, map[string]json.RawMessage{"key": json.RawMessage(`1`)}, map[string]string{"key": "secret"}); !errors.Is(err, ErrPluginInvalid) {
		t.Fatal(err)
	}
	for _, err := range []error{d.RemovePlugin(ctx, "missing"), d.SetPluginGrants(ctx, "missing", nil), d.SetPluginConfiguration(ctx, "missing", nil, nil), d.WithPluginSecrets(ctx, "missing", nil)} {
		if !errors.Is(err, ErrPluginNotFound) {
			t.Fatal(err)
		}
	}
	if _, err := d.PluginDataDir(ctx, "missing"); !errors.Is(err, ErrPluginNotFound) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := d.SetPluginConfiguration(cancelled, desc.ID, nil, nil); !errors.Is(err, ErrPluginState) {
		t.Fatal(err)
	}
}

func TestPluginClosedDatabase(t *testing.T) {
	d, desc, instances := pluginFixture(t)
	ctx := t.Context()
	requirePluginOK(t, d.Close())
	_, getErr := d.GetPlugin(ctx, desc.ID)
	_, listErr := d.ListPlugins(ctx)
	_, dataErr := d.PluginDataDir(ctx, desc.ID)
	for _, err := range []error{getErr, listErr, dataErr,
		d.DiscoverPlugin(ctx, desc, "/bin/plugin", strings.Repeat("a", 64), instances),
		d.RemovePlugin(ctx, desc.ID), d.DeletePluginPermanently(ctx, desc.ID),
		d.SetPluginConfiguration(ctx, desc.ID, nil, nil), d.WithPluginSecrets(ctx, desc.ID, nil),
	} {
		if !errors.Is(err, ErrPluginState) {
			t.Fatalf("unsafe database error: %v", err)
		}
	}
}

func TestPluginStorageFailures(t *testing.T) {
	t.Run("directory unavailable", func(t *testing.T) {
		d, desc, _ := pluginFixture(t)
		d.dataDir = ""
		if err := d.SetPluginConfiguration(t.Context(), desc.ID, nil, map[string]string{"key": "private"}); !errors.Is(err, ErrPluginState) {
			t.Fatal(err)
		}
		if got := d.RedactPluginDiagnostics(desc.ID, "private"); got != "[diagnostics unavailable]" {
			t.Fatal(got)
		}
		if err := d.DeletePluginPermanently(t.Context(), desc.ID); !errors.Is(err, ErrPluginState) {
			t.Fatal(err)
		}
		p, err := d.GetPlugin(t.Context(), desc.ID)
		requirePluginOK(t, err)
		if len(p.Configuration.Secrets) != 0 {
			t.Fatal("failed write changed configuration")
		}
	})
	for _, field := range []string{"description_json", "config_json", "working_config_json"} {
		t.Run(field, func(t *testing.T) {
			d, desc, _ := pluginFixture(t)
			_, err := d.db.Exec(`UPDATE plugin_registration SET ` + field + `='private-invalid-json'`)
			requirePluginOK(t, err)
			if _, err := d.GetPlugin(t.Context(), desc.ID); !errors.Is(err, ErrPluginState) {
				t.Fatalf("corrupt state error: %v", err)
			}
			if _, err := d.ListPlugins(t.Context()); !errors.Is(err, ErrPluginState) {
				t.Fatal(err)
			}
		})
	}
	for _, field := range []string{"public", "secret"} {
		t.Run("oversized "+field, func(t *testing.T) {
			d, desc, _ := pluginFixture(t)
			values := map[string]json.RawMessage{}
			secrets := map[string]string{}
			if field == "public" {
				values["key"] = json.RawMessage(`"` + strings.Repeat("x", 1<<20) + `"`)
			} else {
				secrets["key"] = strings.Repeat("x", 1<<20)
			}
			if err := d.SetPluginConfiguration(t.Context(), desc.ID, values, secrets); !errors.Is(err, ErrPluginInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestPluginConcurrentSecretPatchesAndIsolation(t *testing.T) {
	d, desc, instances := pluginFixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	for _, key := range []string{"first", "second"} {
		wg.Go(func() {
			if err := d.SetPluginConfiguration(ctx, desc.ID, nil, map[string]string{key: key + "-secret"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	requirePluginOK(t, d.WithPluginSecrets(ctx, desc.ID, func(s map[string]string) error {
		if len(s) != 2 {
			t.Error("concurrent update lost secret")
		}
		return nil
	}))
	other := desc
	other.ID = "io.ocman.other"
	requirePluginOK(t, d.DiscoverPlugin(ctx, other, "/bin/other", strings.Repeat("a", 64), instances))
	requirePluginOK(t, d.WithPluginSecrets(ctx, other.ID, func(s map[string]string) error {
		if len(s) != 0 {
			t.Error("cross-plugin secret disclosure")
		}
		return nil
	}))
	root, err := d.pluginDirectory(desc.ID, "plugin-secrets")
	requirePluginOK(t, err)
	path := root.Name()
	requirePluginOK(t, root.Close())
	requirePluginOK(t, d.DeletePluginPermanently(ctx, desc.ID))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secret history not deleted: %v", err)
	}
	if _, err := d.GetPlugin(ctx, other.ID); err != nil {
		t.Fatal("deletion affected another plugin")
	}
}

func TestPluginDiagnosticsPartialSecret(t *testing.T) {
	d, desc, _ := pluginFixture(t)
	requirePluginOK(t, d.SetPluginConfiguration(t.Context(), desc.ID, nil, map[string]string{"token": "secret-with-newline\nsecond-line"}))
	for _, text := range []string{"secret-with-new", "secret-with-newline\nsecond", "secret-with-newline\\nsecond"} {
		if got := d.RedactPluginDiagnostics(desc.ID, "stderr: "+text); got != "stderr: [REDACTED]" {
			t.Fatalf("partial secret: %q", got)
		}
	}
}
