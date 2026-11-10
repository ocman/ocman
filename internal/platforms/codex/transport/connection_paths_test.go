package codextransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestNamedConnectionRefusesRuntimeAliases(t *testing.T) {
	for level := range 4 {
		for _, shape := range []string{"symlink", "file", "public"} {
			t.Run(shape+"/"+[]string{"collection", "connection", "runtime", "supervisor"}[level], func(t *testing.T) {
				cfg := Config{DataDir: t.TempDir(), Connection: &ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: t.TempDir()}}
				parts := []string{cfg.DataDir, "platform-connections", cfg.Connection.ID, "runtime", "codex-supervisor"}
				target := filepath.Join(parts[:level+2]...)
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				switch shape {
				case "symlink":
					if err := os.Symlink(outside, target); err != nil {
						t.Fatal(err)
					}
				case "file":
					if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "public":
					if err := os.Mkdir(target, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(target, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := resolvePlatformPaths(t.Context(), cfg); err == nil {
					t.Fatal("accepted an aliased or unsafe runtime root")
				}
				if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
					t.Fatalf("preparation wrote through a symlink: %v %v", entries, err)
				}
			})
		}
	}
}

func TestNamedConnectionUnavailablePathsAndCancellation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ home, data string }{
		{file, t.TempDir()},
		{filepath.Join(t.TempDir(), "missing"), t.TempDir()},
		{t.TempDir(), file},
		{t.TempDir(), filepath.Join(t.TempDir(), "missing")},
	} {
		cfg := Config{DataDir: tc.data, Connection: &ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: tc.home}}
		if _, err := resolvePlatformPaths(t.Context(), cfg); err == nil {
			t.Fatalf("unavailable named configuration was accepted: %+v", tc)
		}
	}
	cfg := Config{DataDir: t.TempDir(), Connection: &ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: t.TempDir()}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolvePlatformPaths(ctx, cfg); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(cfg.DataDir); err != nil || len(entries) != 0 {
		t.Fatalf("canceled construction changed owner state: %v %v", entries, err)
	}
}

func TestNamedConnectionCanonicalHomeAndStablePaths(t *testing.T) {
	home, base := t.TempDir(), t.TempDir()
	link := filepath.Join(t.TempDir(), "account-link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: base, Connection: &ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: link}}
	first, err := resolvePlatformPaths(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolvePlatformPaths(t.Context(), cfg)
	canonical, homeErr := filepath.EvalSymlinks(home)
	if err != nil || homeErr != nil || first != second || first.home != canonical {
		t.Fatalf("canonical/restarted identity changed: %+v %+v %v %v", first, second, err, homeErr)
	}
}
