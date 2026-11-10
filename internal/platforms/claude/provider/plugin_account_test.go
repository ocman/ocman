package claudeprovider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountPluginOperationsFilterEnvironment(t *testing.T) {
	dir := t.TempDir()
	cli, log := filepath.Join(dir, "claude"), filepath.Join(dir, "environment")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n/usr/bin/env > \"$PLUGIN_ENV_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGIN_ENV_LOG", log)
	t.Setenv("CLAUDE_CONFIG_DIR", "/owner")
	t.Setenv("ANTHROPIC_API_KEY", "owner-secret")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	config, marketplace := filepath.Join(dir, "config"), filepath.Join(dir, "marketplace")
	if _, err := WritePlugin(marketplace, nil, ""); err != nil {
		t.Fatal(err)
	}
	for _, run := range []func() error{
		func() error { return InstallAccountPlugin(t.Context(), cli, config, marketplace) },
		func() error { _, err := EnsureAccountPlugin(t.Context(), cli, config, marketplace); return err },
		func() error { return UninstallAccountPlugin(t.Context(), cli, config) },
	} {
		if err := run(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		env := string(data)
		if !strings.Contains(env, "CLAUDE_CONFIG_DIR="+config+"\n") || strings.Contains(env, "ANTHROPIC_API_KEY=") || strings.Contains(env, "CLAUDE_CODE_USE_BEDROCK=") {
			t.Fatal("plugin used inherited account configuration or auth")
		}
	}
}
