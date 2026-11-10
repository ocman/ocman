package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigAuthSelectsOnlyParsedCredentialSources(t *testing.T) {
	t.Setenv("CONFIG_REGRESSION_PASSWORD", "intended-password")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nstartup:\n  auth-password: secret://env/CONFIG_REGRESSION_PASSWORD\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  string
		args []string
	}{
		{name: "newline-only environment", env: "\r\n"},
		{name: "after terminator", args: []string{"--", "--auth-password=ignored"}},
		{name: "after positional", args: []string{"position", "--auth-password=ignored"}},
		{name: "flag-looking value", args: []string{"--log-file", "--auth-password=not-a-flag"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(authPasswordEnv, tc.env)
			fs := configTestFlags()
			fs.String("log-file", "", "")
			if _, err := prepareFileConfig(fs, tc.args, path); err != nil {
				t.Fatal(err)
			}
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if err := resolveConfigAuth(fs, tc.args); err != nil {
				t.Fatal(err)
			}
			actual, err := resolveAuthPassword(fs.Lookup("auth-password").Value.String(), "")
			if err != nil || actual != "intended-password" {
				t.Fatalf("authentication did not use the resolved credential: %v", err)
			}
		})
	}
}

func TestConfigPathIgnoresUnparsedFlagTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, args := range [][]string{
		{"position", "--config=/ignored/config.yaml"},
		{"--log-file", "--config=/not-a-config.yaml"},
	} {
		fs := configTestFlags()
		fs.String("log-file", "", "")
		got, err := prepareFileConfig(fs, args, path)
		if err != nil || got != path {
			t.Fatalf("selected unparsed configuration flag: %q, %v", got, err)
		}
	}
}
