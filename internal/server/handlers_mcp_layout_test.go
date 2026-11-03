package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureOpenCodeMCPLayout(t *testing.T) {
	const flat = `{"mcp":{"ocman":{"type":"remote","url":"http://127.0.0.1:8227/mcp","enabled":true}}}`
	cases := []struct {
		name     string
		file     string
		moved    bool
		modified bool
	}{
		{name: "moves the flat entry", file: "opencode.json", moved: true, modified: true},
		{name: "refuses JSONC", file: "opencode.jsonc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			t.Setenv("OPENCODE_CONFIG", "")
			path := filepath.Join(dir, "opencode", tc.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(flat), 0o600); err != nil {
				t.Fatal(err)
			}

			ensureOpenCodeMCPLayout("http://127.0.0.1:8227/mcp", "")

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(raw), `"servers"`) && strings.Contains(string(raw), `"codemode": false`); got != tc.moved {
				t.Errorf("moved to v2 layout = %v, want %v: %s", got, tc.moved, raw)
			}
			if (string(raw) != flat) != tc.modified {
				t.Errorf("modified = %v, want %v: %s", string(raw) != flat, tc.modified, raw)
			}
		})
	}
}

func TestEnsureOpenCodeMCPLayoutReportsUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// A directory where the config file should be makes the read fail
	// with an error other than "not editable"; startup must survive it.
	t.Setenv("OPENCODE_CONFIG", dir)
	ensureOpenCodeMCPLayout("http://127.0.0.1:8227/mcp", "")
}
