package forgejo

import (
	"os"
	"path/filepath"
	"testing"
)

// teaSample is the canonical tea config format we expect to parse.
// Mirrors the file `tea login add` writes.
const teaSample = `logins:
  - name: codeberg
    url: https://codeberg.org
    token: CODEBERG_TOKEN
    default: true
  - name: internal
    url: https://code.example.com
    token: INTERNAL_TOKEN
    ssh_host: ""
    insecure: false
`

func writeTempTeaConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write tea config: %v", err)
	}
	return path
}

func TestParseTeaConfig_ExtractsAllLogins(t *testing.T) {
	path := writeTempTeaConfig(t, teaSample)
	logins, err := parseTeaConfig(path)
	if err != nil {
		t.Fatalf("parseTeaConfig: %v", err)
	}
	if len(logins) != 2 {
		t.Fatalf("expected 2 logins, got %d", len(logins))
	}

	want := []Login{
		{Name: "codeberg", URL: "https://codeberg.org", Host: "codeberg.org", Token: "CODEBERG_TOKEN"},
		{Name: "internal", URL: "https://code.example.com", Host: "code.example.com", Token: "INTERNAL_TOKEN"},
	}
	for i, l := range logins {
		if l != want[i] {
			t.Errorf("login[%d] = %+v, want %+v", i, l, want[i])
		}
	}
}

func TestParseTeaConfig_MissingFileNotAnError(t *testing.T) {
	// No tea installed / no logins configured — return an empty slice,
	// not an error. The caller treats "no logins" the same as "tea not
	// in use" and silently moves on.
	logins, err := parseTeaConfig(filepath.Join(t.TempDir(), "does-not-exist.yml"))
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if len(logins) != 0 {
		t.Errorf("missing file: expected 0 logins, got %d", len(logins))
	}
}

func TestParseTeaConfig_SkipsIncompleteLogins(t *testing.T) {
	body := `logins:
  - name: ok
    url: https://example.com
    token: TOK
  - name: no-token
    url: https://nope.example.com
  - name: no-url
    token: STRAY
  - name: bad-url
    url: ":::not-a-url"
    token: TOK
`
	path := writeTempTeaConfig(t, body)
	logins, err := parseTeaConfig(path)
	if err != nil {
		t.Fatalf("parseTeaConfig: %v", err)
	}
	if len(logins) != 1 {
		t.Fatalf("expected 1 login (others skipped), got %d", len(logins))
	}
	if logins[0].Name != "ok" {
		t.Errorf("expected 'ok' login, got %+v", logins[0])
	}
}

func TestTeaLogins_ResolvesXDGConfig(t *testing.T) {
	dir := t.TempDir()
	teaDir := filepath.Join(dir, "tea")
	if err := os.MkdirAll(teaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teaDir, "config.yml"), []byte(teaSample), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", t.TempDir()) // ensure HOME fallback is not used

	logins, err := TeaLogins()
	if err != nil {
		t.Fatalf("TeaLogins: %v", err)
	}
	if len(logins) != 2 {
		t.Fatalf("expected 2 logins, got %d", len(logins))
	}
}
