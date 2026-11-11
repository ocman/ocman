package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/relay"
	"github.com/ocman/ocman/internal/share"
)

func TestConfigureSSO(t *testing.T) {
	t.Setenv("OCMAN_RELAY_SSO_CONFIG", "")
	cfg := relay.Config{}
	if err := configureSSO(&cfg); err != nil || cfg.EnrollmentAuth != nil {
		t.Fatal("manual configuration changed")
	}
	path := filepath.Join(t.TempDir(), "sso.json")
	t.Setenv("OCMAN_RELAY_SSO_CONFIG", path)
	if err := configureSSO(&cfg); err == nil {
		t.Fatal("missing config accepted")
	}
	if err := os.WriteFile(path, []byte(`invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureSSO(&cfg); err == nil {
		t.Fatal("invalid config accepted")
	}
	data := `[{"id":"github","name":"GitHub","kind":"github","clientId":"github-id","clientSecretEnv":"TEST_GITHUB_SECRET"},{"id":"google","name":"Google","kind":"google","clientId":"google-id","clientSecretEnv":"TEST_GOOGLE_SECRET"}]`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCMAN_RELAY_PUBLIC_URL", "https://relay.example")
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store = store
	t.Setenv("TEST_GITHUB_SECRET", "github-secret")
	t.Setenv("TEST_GOOGLE_SECRET", "")
	if err := configureSSO(&cfg); err == nil {
		t.Fatal("missing provider secret accepted")
	}
	t.Setenv("TEST_GOOGLE_SECRET", "google-secret")
	if err := configureSSO(&cfg); err != nil || cfg.EnrollmentAuth == nil {
		t.Fatalf("configure: %v", err)
	}
}
