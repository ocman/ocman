package main

import (
	"os"
	"path/filepath"
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

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8228", true},
		{"[::1]:8228", true},
		{"localhost:8228", true},
		{":8228", true}, // bare port = all loopback by convention for this check
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
