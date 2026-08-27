package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAuthPassword_Precedence pins the env > file > flag
// ordering that the documentation promises. A regression here would
// silently let an operator think they're using one source while
// actually honoring another.
func TestResolveAuthPassword_Precedence(t *testing.T) {
	dir := t.TempDir()
	passwdFile := filepath.Join(dir, "passwd")
	if err := os.WriteFile(passwdFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write passwd: %v", err)
	}

	tests := []struct {
		name       string
		env        string // "" means unset
		fileValue  string
		flagValue  string
		want       string
		setEnvFlag bool // distinguishes "" from unset
	}{
		{"env wins over everything", "from-env", passwdFile, "from-flag", "from-env", true},
		{"file wins when env unset", "", passwdFile, "from-flag", "from-file", false},
		{"flag used when nothing else set", "", "", "from-flag", "from-flag", false},
		{"empty env treated as unset", "", passwdFile, "from-flag", "from-file", true},
		{"all empty returns empty", "", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnvFlag {
				t.Setenv(authPasswordEnv, tt.env)
			} else {
				os.Unsetenv(authPasswordEnv)
			}
			got, err := resolveAuthPassword(tt.flagValue, tt.fileValue)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEmbeddedFactorySkillUsesOnlyFactoryContract(t *testing.T) {
	skills := embeddedSkills()
	source := string(skills["ocman-factory"])
	for _, required := range []string{
		"explicit", "prepare_factory_work", "acknowledge_factory_execution",
		"create_factory_work_epic", "confirmation", "stop",
	} {
		if !strings.Contains(strings.ToLower(source), strings.ToLower(required)) {
			t.Errorf("Factory skill is missing %q", required)
		}
	}
	for _, hidden := range []string{"beads", "sqlite"} {
		if strings.Contains(strings.ToLower(source), hidden) {
			t.Errorf("Factory skill exposes implementation term %q", hidden)
		}
	}
	if len(skills) != 2 || len(skills["ocman-workflows"]) == 0 {
		t.Fatalf("embedded skills = %#v", skills)
	}
}

func TestResolveOpenCodePassword(t *testing.T) {
	dir := t.TempDir()
	passwordFile := filepath.Join(dir, "opencode-password")
	if err := os.WriteFile(passwordFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(opencodeServerPasswordEnv, "from-env")
	got, err := resolveOpenCodePassword(passwordFile, true)
	if err != nil || got != "from-env" {
		t.Fatalf("env precedence: password=%q err=%v", got, err)
	}

	t.Setenv(opencodeServerPasswordEnv, "")
	got, err = resolveOpenCodePassword(passwordFile, true)
	if err != nil || got != "from-file" {
		t.Fatalf("file precedence: password=%q err=%v", got, err)
	}

	got, err = resolveOpenCodePassword("", true)
	if err != nil || got == "" {
		t.Fatalf("generated password: password empty=%v err=%v", got == "", err)
	}
	if _, err := base64.RawURLEncoding.DecodeString(got); err != nil {
		t.Fatalf("generated password is not raw URL-safe base64: %v", err)
	}

	got, err = resolveOpenCodePassword("", false)
	if err != nil || got != "" {
		t.Fatalf("disabled: password=%q err=%v", got, err)
	}
}

// TestResolveAuthPassword_FileMissing surfaces a clear error when
// -auth-password-file points at a file that doesn't exist, instead of
// silently falling through to the flag.
func TestResolveAuthPassword_FileMissing(t *testing.T) {
	os.Unsetenv(authPasswordEnv)
	_, err := resolveAuthPassword("from-flag", "/nonexistent/does/not/exist")
	if err == nil {
		t.Error("expected error for missing password file")
	}
}

// TestResolveAuthPassword_TrimsTrailingNewline ensures the common
// `echo secret > passwd` pattern produces the expected password.
func TestResolveAuthPassword_TrimsTrailingNewline(t *testing.T) {
	os.Unsetenv(authPasswordEnv)
	dir := t.TempDir()
	p := filepath.Join(dir, "passwd")
	if err := os.WriteFile(p, []byte("  secret\n\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := resolveAuthPassword("", p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Leading whitespace preserved; trailing trimmed.
	if got != "  secret" {
		t.Errorf("got %q, want %q", got, "  secret")
	}
}

// TestParseBoolEnv covers the accepted truthy spellings and makes
// sure empty / garbage values stay falsy so OCMAN_AUTH_TRUST_LOCALHOST=
// doesn't accidentally enable the bypass.
func TestParseBoolEnv(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "True", "yes", "YES", "on", " on ", " 1\n"}
	for _, v := range truthy {
		if !parseBoolEnv(v) {
			t.Errorf("parseBoolEnv(%q) = false, want true", v)
		}
	}
	falsy := []string{"", " ", "0", "false", "no", "off", "maybe", "enable", "disabled"}
	for _, v := range falsy {
		if parseBoolEnv(v) {
			t.Errorf("parseBoolEnv(%q) = true, want false", v)
		}
	}
}

func TestValidateRemoteTransport(t *testing.T) {
	tests := []struct {
		name                  string
		listenAddr, cert, key string
		trustedOverlay        bool
		wantErr               bool
	}{
		{"remote disabled", "", "", "", false, false},
		{"TLS", "0.0.0.0:8230", "cert.pem", "key.pem", false, false},
		{"trusted overlay", "0.0.0.0:8230", "", "", true, false},
		{"accidental plaintext", "0.0.0.0:8230", "", "", false, true},
		{"certificate only", "0.0.0.0:8230", "cert.pem", "", false, true},
		{"key only", "0.0.0.0:8230", "", "key.pem", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRemoteTransport(tt.listenAddr, tt.cert, tt.key, tt.trustedOverlay)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRemoteTransport() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateListenExposure pins the startup refusal: a listener
// reachable from off-box with no password configured is an open remote
// control for the operator's coding agents, so ocman must refuse to
// start unless the operator explicitly opts out.
func TestValidateListenExposure(t *testing.T) {
	tests := []struct {
		name           string
		addr           string
		authConfigured bool
		allowInsecure  bool
		wantErr        bool
	}{
		{"loopback without auth still starts", "127.0.0.1:8228", false, false, false},
		{"loopback name without auth still starts", "localhost:8228", false, false, false},
		{"ipv6 loopback without auth still starts", "[::1]:8228", false, false, false},
		{"wildcard without auth refuses", "0.0.0.0:8228", false, false, true},
		{"bare port without auth refuses", ":8228", false, false, true},
		{"lan address without auth refuses", "192.168.1.10:8228", false, false, true},
		{"wildcard with auth starts", "0.0.0.0:8228", true, false, false},
		{"wildcard with explicit opt-out starts", "0.0.0.0:8228", false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateListenExposure(tt.addr, tt.authConfigured, tt.allowInsecure)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateListenExposure(%q, auth=%v, insecure=%v) error = %v, wantErr %v",
					tt.addr, tt.authConfigured, tt.allowInsecure, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "-insecure-no-auth") {
				t.Errorf("error must name the opt-out flag, got: %v", err)
			}
		})
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8228", true},
		{"[::1]:8228", true},
		{"localhost:8228", true},
		{":8228", false}, // bare port binds every interface, not just loopback
		{"0.0.0.0:8228", false},
		{"192.168.1.10:8228", false},
		{"[2001:db8::1]:8228", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := isLoopbackAddr(tt.addr); got != tt.want {
				t.Errorf("isLoopbackAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
