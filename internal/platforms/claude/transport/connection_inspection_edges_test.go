package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeInspectionRejectsUnavailableAccount(t *testing.T) {
	for _, tc := range []struct {
		name      string
		account   bool
		directory string
		message   string
	}{
		{"legacy", false, "", "legacy"},
		{"missing", true, filepath.Join(t.TempDir(), "absent"), "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &claudePlatform{cli: "must-not-execute", configDir: tc.directory, connectionAccount: tc.account}
			got, err := p.InspectConnection(t.Context())
			if err != nil || got.Ready || got.IsolationSupported || !strings.Contains(got.Message, tc.message) {
				t.Fatalf("inspection = %+v, %v", got, err)
			}
		})
	}
	t.Setenv("PATH", t.TempDir())
	got, err := InspectConfiguration(t.Context(), t.TempDir())
	if err != nil || got.Ready || !strings.Contains(got.Message, "Install") {
		t.Fatalf("missing CLI = %+v, %v", got, err)
	}
}

func TestClaudeInspectionSettingsFilePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, file, data, kind string
		supported              bool
	}{
		{"native-metadata", ".claude.json", `{"oauthAccount":{"emailAddress":"person@example.invalid"}}`, "file", true},
		{"safe-env", "settings.json", `{"env":{"EDITOR":"vi"}}`, "file", true},
		{"malformed-env", "settings.json", `{"env":"not-an-object"}`, "file", false},
		{"managed", "managed-settings.json", `{}`, "file", false},
		{"policy", "policy-limits.json", `{}`, "file", false},
		{"remote-policy", "remote-settings.json", `{"theme":"dark"}`, "file", false},
		{"empty-remote-policy", "remote-settings.json", `{}`, "file", true},
		{"directory", "settings.json", "", "directory", false},
		{"credentials-link", ".credentials.json", `{}`, "link", false},
		{"settings-link", "settings.json", `{}`, "link", false},
		{"oversized", "config.json", strings.Repeat(" ", (1<<20)+1), "file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := t.TempDir()
			path := filepath.Join(config, tc.file)
			var err error
			switch tc.kind {
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "link":
				err = os.Symlink(filepath.Join(config, "absent"), path)
			default:
				err = os.WriteFile(path, []byte(tc.data), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := claudeInspectionSettings(config); got != tc.supported {
				t.Fatalf("supported = %v, want %v", got, tc.supported)
			}
		})
	}
}

func TestClaudeInspectionCanceledAndTempFailure(t *testing.T) {
	config := canonical(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &claudePlatform{cli: filepath.Join(config, "never-execute"), configDir: config, connectionAccount: true}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.InspectConnection(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "absent"))
	if _, err := p.InspectConnection(t.Context()); err == nil {
		t.Fatal("missing temporary root accepted")
	}
}
