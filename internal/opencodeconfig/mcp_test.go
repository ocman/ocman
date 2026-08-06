package opencodeconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// configHome points config resolution at a temp dir for the whole test.
func configHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("OPENCODE_CONFIG", "")
	return filepath.Join(dir, "opencode")
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPath(t *testing.T) {
	t.Run("OPENCODE_CONFIG wins", func(t *testing.T) {
		configHome(t)
		t.Setenv("OPENCODE_CONFIG", "/tmp/custom.json")
		got, err := Path()
		if err != nil || got != "/tmp/custom.json" {
			t.Fatalf("Path() = %q, %v", got, err)
		}
	})
	t.Run("defaults to opencode.json", func(t *testing.T) {
		dir := configHome(t)
		got, err := Path()
		if err != nil || got != filepath.Join(dir, "opencode.json") {
			t.Fatalf("Path() = %q, %v", got, err)
		}
	})
	t.Run("prefers an existing jsonc over a missing json", func(t *testing.T) {
		dir := configHome(t)
		writeConfig(t, filepath.Join(dir, "opencode.jsonc"), "{}")
		got, err := Path()
		if err != nil || got != filepath.Join(dir, "opencode.jsonc") {
			t.Fatalf("Path() = %q, %v", got, err)
		}
	})
	t.Run("prefers json when both exist", func(t *testing.T) {
		dir := configHome(t)
		writeConfig(t, filepath.Join(dir, "opencode.jsonc"), "{}")
		writeConfig(t, filepath.Join(dir, "opencode.json"), "{}")
		got, _ := Path()
		if got != filepath.Join(dir, "opencode.json") {
			t.Fatalf("Path() = %q", got)
		}
	})
}

func TestCheck(t *testing.T) {
	const want = "http://127.0.0.1:8227/mcp"
	tests := []struct {
		name           string
		file           string // "" = no config file
		body           string
		wantConfigured bool
		wantCurrent    string
		wantEditable   bool
	}{
		{
			name:         "no config file yet",
			wantEditable: true,
		},
		{
			name:         "empty object",
			file:         "opencode.json",
			body:         `{}`,
			wantEditable: true,
		},
		{
			name:         "other servers only",
			file:         "opencode.json",
			body:         `{"mcp":{"other":{"type":"remote","url":"http://x/mcp"}}}`,
			wantEditable: true,
		},
		{
			name:           "configured and current",
			file:           "opencode.json",
			body:           `{"mcp":{"ocman":{"type":"remote","url":"` + want + `","enabled":true}}}`,
			wantConfigured: true,
			wantCurrent:    want,
			wantEditable:   true,
		},
		{
			name:           "enabled omitted counts as enabled",
			file:           "opencode.json",
			body:           `{"mcp":{"ocman":{"type":"remote","url":"` + want + `"}}}`,
			wantConfigured: true,
			wantCurrent:    want,
			wantEditable:   true,
		},
		{
			name:         "explicitly disabled",
			file:         "opencode.json",
			body:         `{"mcp":{"ocman":{"type":"remote","url":"` + want + `","enabled":false}}}`,
			wantCurrent:  want,
			wantEditable: true,
		},
		{
			name:         "stale url",
			file:         "opencode.json",
			body:         `{"mcp":{"ocman":{"type":"remote","url":"http://localhost:8228/mcp","enabled":true}}}`,
			wantCurrent:  "http://localhost:8228/mcp",
			wantEditable: true,
		},
		{
			name:         "jsonc is read but not editable",
			file:         "opencode.jsonc",
			body:         "{\n  // mine\n  \"mcp\": {}\n}",
			wantEditable: false,
		},
		{
			name:         "json with comments is not editable",
			file:         "opencode.json",
			body:         "{\n  // sneaky\n  \"mcp\": {}\n}",
			wantEditable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := configHome(t)
			if tt.file != "" {
				writeConfig(t, filepath.Join(dir, tt.file), tt.body)
			}
			st, err := Check(want)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if st.Configured != tt.wantConfigured {
				t.Errorf("Configured = %v, want %v (reason %q)", st.Configured, tt.wantConfigured, st.Reason)
			}
			if st.CurrentURL != tt.wantCurrent {
				t.Errorf("CurrentURL = %q, want %q", st.CurrentURL, tt.wantCurrent)
			}
			if st.Editable != tt.wantEditable {
				t.Errorf("Editable = %v, want %v (reason %q)", st.Editable, tt.wantEditable, st.Reason)
			}
			if !st.Editable && st.Reason == "" {
				t.Error("not editable but no reason given")
			}
			if st.WantURL != want {
				t.Errorf("WantURL = %q", st.WantURL)
			}
		})
	}
}

func TestInstallCreatesConfig(t *testing.T) {
	dir := configHome(t)
	const want = "http://127.0.0.1:8227/mcp"

	backup, err := Install(want)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if backup != "" {
		t.Errorf("backup = %q, want none for a fresh config", backup)
	}

	st, err := Check(want)
	if err != nil || !st.Configured {
		t.Fatalf("after Install: configured=%v err=%v", st.Configured, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "https://opencode.ai/config.json") {
		t.Errorf("fresh config should carry $schema: %s", raw)
	}
}

func TestInstallPreservesOtherKeysAndBacksUp(t *testing.T) {
	dir := configHome(t)
	path := filepath.Join(dir, "opencode.json")
	original := `{"$schema":"https://opencode.ai/config.json","theme":"catppuccin","mcp":{"other":{"type":"local","command":["x"],"enabled":true}}}`
	writeConfig(t, path, original)

	const want = "http://127.0.0.1:8227/mcp"
	backup, err := Install(want)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The backup must hold the original bytes, byte for byte.
	if backup == "" {
		t.Fatal("no backup taken for an existing config")
	}
	if !strings.HasSuffix(backup, "-backup.json") {
		t.Errorf("backup name %q lacks the -backup.json suffix", backup)
	}
	backedUp, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(backedUp) != original {
		t.Errorf("backup = %s, want the original bytes", backedUp)
	}

	var doc struct {
		Schema string `json:"$schema"`
		Theme  string `json:"theme"`
		MCP    map[string]struct {
			Type    string   `json:"type"`
			URL     string   `json:"url"`
			Command []string `json:"command"`
			Enabled bool     `json:"enabled"`
		} `json:"mcp"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("rewritten config is invalid JSON: %v\n%s", err, raw)
	}
	if doc.Theme != "catppuccin" || doc.Schema == "" {
		t.Errorf("unrelated keys lost: %+v", doc)
	}
	if other := doc.MCP["other"]; other.Type != "local" || len(other.Command) != 1 {
		t.Errorf("other MCP server mangled: %+v", other)
	}
	if ours := doc.MCP[ServerName]; ours.URL != want || ours.Type != "remote" || !ours.Enabled {
		t.Errorf("ocman entry = %+v", ours)
	}
}

func TestInstallUpdatesStaleURL(t *testing.T) {
	dir := configHome(t)
	path := filepath.Join(dir, "opencode.json")
	writeConfig(t, path, `{"mcp":{"ocman":{"type":"remote","url":"http://localhost:8228/mcp","enabled":true}}}`)

	const want = "http://127.0.0.1:8227/mcp"
	if _, err := Install(want); err != nil {
		t.Fatalf("Install: %v", err)
	}
	st, _ := Check(want)
	if !st.Configured || st.CurrentURL != want {
		t.Fatalf("stale entry not updated: %+v", st)
	}
}

func TestInstallRefusesNonEditable(t *testing.T) {
	for name, file := range map[string]string{
		"jsonc":              "opencode.jsonc",
		"json with comments": "opencode.json",
	} {
		t.Run(name, func(t *testing.T) {
			dir := configHome(t)
			path := filepath.Join(dir, file)
			body := "{\n  // hand-written\n  \"theme\": \"x\"\n}"
			writeConfig(t, path, body)

			if _, err := Install("http://127.0.0.1:8227/mcp"); !errors.Is(err, ErrNotEditable) {
				t.Fatalf("Install err = %v, want ErrNotEditable", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != body {
				t.Errorf("file was modified despite the refusal: %s", raw)
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.Contains(e.Name(), "-backup.json") {
					t.Errorf("took a backup %q despite refusing", e.Name())
				}
			}
		})
	}
}

func TestInstallRefusesNonObjectMCP(t *testing.T) {
	dir := configHome(t)
	writeConfig(t, filepath.Join(dir, "opencode.json"), `{"mcp":"nope"}`)
	if _, err := Install("http://127.0.0.1:8227/mcp"); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("Install err = %v, want ErrNotEditable", err)
	}
}

// Two installs on the same day must not clobber the pristine original.
func TestBackupPathIncludesTime(t *testing.T) {
	base := "/cfg/opencode.json"
	first := backupPath(base, time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC))
	second := backupPath(base, time.Date(2026, 8, 6, 17, 30, 15, 0, time.UTC))
	if first == second {
		t.Fatalf("same backup path for different times: %s", first)
	}
	const wantName = "/cfg/opencode.2026-08-06T100000-backup.json"
	if first != wantName {
		t.Errorf("backupPath = %q, want %q", first, wantName)
	}
	// A .jsonc original backs up to the same obvious .json sibling.
	if got := backupPath("/cfg/opencode.jsonc", time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)); got != wantName {
		t.Errorf("jsonc backup = %q, want %q", got, wantName)
	}
}
