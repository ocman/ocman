package app

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/configfile"
	"github.com/ocman/ocman/internal/state"
)

func configTestFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("config-test", flag.ContinueOnError)
	fs.String("addr", "default:8228", "")
	fs.String("platforms", "opencode,claudecode", "")
	fs.String("auth-password", "", "")
	fs.String("auth-password-file", "", "")
	fs.String("config", "", "")
	fs.Bool("gui", true, "")
	for name := range startupEnvironment {
		if fs.Lookup(name) == nil {
			fs.String(name, "", "")
		}
	}
	return fs
}

func TestPrepareFileConfigPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nstartup:\n  addr: file:8228\n  platforms: claudecode\n  gui: false\nsettings: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs := configTestFlags()
	selected, err := prepareFileConfig(fs, nil, path)
	if err != nil || selected != path || fs.Lookup("addr").Value.String() != "file:8228" || fs.Lookup("gui").Value.String() != "false" {
		t.Fatalf("file config not applied: %s, %v", selected, err)
	}
	t.Setenv("OCMAN_ADDR", "env:8228")
	fs = configTestFlags()
	args := []string{"--config=" + path, "--addr=cli:8228"}
	if _, err := prepareFileConfig(fs, args, filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
		t.Fatal(err)
	}
	if fs.Lookup("addr").Value.String() != "env:8228" {
		t.Fatal("environment did not override file")
	}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if fs.Lookup("addr").Value.String() != "cli:8228" {
		t.Fatal("CLI did not override environment")
	}
}

func TestPrepareFileConfigSecretReferences(t *testing.T) {
	t.Setenv("OCMAN_AUTH_PASSWORD", "")
	t.Setenv("CONFIG_STARTUP_PASSWORD", "test-secret")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nstartup:\n  auth-password: secret://env/CONFIG_STARTUP_PASSWORD\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs := configTestFlags()
	if _, err := prepareFileConfig(fs, nil, path); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfigAuth(fs, nil); err != nil || fs.Lookup("auth-password").Value.String() != "test-secret" {
		t.Fatalf("startup secret failed: %v", err)
	}
	t.Setenv("CONFIG_STARTUP_PASSWORD", "")
	fs = configTestFlags()
	if _, err := prepareFileConfig(fs, nil, path); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfigAuth(fs, nil); err == nil {
		t.Fatal("accepted unavailable secret")
	}
	t.Setenv("OCMAN_AUTH_PASSWORD", "winning-secret")
	if err := resolveConfigAuth(fs, nil); err != nil {
		t.Fatal("resolved unused secret instead of honoring environment override")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "test-secret") || strings.Contains(string(data), "winning-secret") {
		t.Fatal("startup resolution rewrote file")
	}
}

func TestPrepareFileConfigErrors(t *testing.T) {
	for _, input := range []string{
		"version: 1\nstartup: {not-a-flag: x}",
		"version: 1\nstartup: {config: somewhere}",
		"version: 1\nstartup: {gui: invalid}",
		"version: 1\nstartup: {auth-password: plaintext}",
		"version: 1\nstartup: {addr: {nested: value}}",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareFileConfig(configTestFlags(), nil, path); err == nil {
			t.Fatalf("accepted invalid startup config: %s", input)
		}
	}
	if _, err := prepareFileConfig(configTestFlags(), []string{"--config"}, ""); err == nil {
		t.Fatal("accepted missing path")
	}
	if _, err := prepareFileConfig(configTestFlags(), []string{"--config="}, ""); err == nil {
		t.Fatal("accepted empty path")
	}
	path := filepath.Join(t.TempDir(), "new.yaml")
	if got, err := prepareFileConfig(configTestFlags(), []string{"--config", path}, ""); err != nil || got != path {
		t.Fatalf("explicit migration path = %s, %v", got, err)
	}
}

func TestDefaultConfigPathIsolation(t *testing.T) {
	t.Setenv("OCMAN_CONFIG", "")
	t.Setenv("OCMAN_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	if got := defaultConfigPath(); got != filepath.Join(filepath.Dir(os.Getenv("OCMAN_STATE_DB")), "config.yaml") {
		t.Fatalf("state isolation not respected: %s", got)
	}
	t.Setenv("OCMAN_STATE_DB", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := defaultConfigPath(); got != filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ocman", "config.yaml") {
		t.Fatalf("XDG path = %s", got)
	}
	t.Setenv("OCMAN_CONFIG", "/explicit/config.yaml")
	if got := defaultConfigPath(); got != "/explicit/config.yaml" {
		t.Fatalf("explicit path = %s", got)
	}
}

func TestStartupOcmanSecretAndWinningSources(t *testing.T) {
	t.Setenv("OCMAN_AUTH_PASSWORD", "")
	dir := t.TempDir()
	dbPath, path := filepath.Join(dir, "state.db"), filepath.Join(dir, "config.yaml")
	t.Setenv("OCMAN_STATE_DB", dbPath)
	store, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(t.Context(), "webhook.enrollment_token", "managed-password"); err != nil {
		t.Fatal(err)
	}
	f := &configfile.File{Path: path}
	if err := f.Update(func(d *configfile.Document) error {
		ref, _, err := d.Get("settings", "webhook.enrollment_token")
		if err != nil {
			return err
		}
		return d.Set("startup", "auth-password", ref)
	}); err != nil {
		t.Fatal(err)
	}
	fs := configTestFlags()
	if _, err := prepareFileConfig(fs, []string{"--"}, path); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfigAuth(fs, nil); err != nil || fs.Lookup("auth-password").Value.String() != "managed-password" {
		t.Fatalf("managed startup secret: %v", err)
	}
	if err := fs.Set("auth-password", "secret://unknown/unused"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("auth-password-file", "/provided-by-cli"); err != nil {
		t.Fatal(err)
	}
	if err := resolveConfigAuth(fs, nil); err != nil {
		t.Fatal("resolved unused reference instead of using password file")
	}
	if !flagPresent(fs, []string{"--auth-password=literal"}, "auth-password") || !flagPresent(fs, []string{"-auth-password", "literal"}, "auth-password") {
		t.Fatal("explicit credential flag not recognized")
	}
	t.Setenv("OCMAN_GUI", "not-a-boolean")
	if _, err := prepareFileConfig(configTestFlags(), nil, path); err == nil {
		t.Fatal("invalid environment override was ignored")
	}
}
