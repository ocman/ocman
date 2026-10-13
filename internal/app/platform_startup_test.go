package app

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type startupPlatform struct{ platforms.Platform }

func (startupPlatform) ID() platforms.ID                     { return "test-provider" }
func (startupPlatform) DisplayName() string                  { return "Test provider" }
func (startupPlatform) Available(context.Context) bool       { return false }
func (startupPlatform) Capabilities() platforms.Capabilities { return platforms.Capabilities{} }
func (startupPlatform) SessionsInactiveBefore(context.Context, int64) ([]db.SessionArchiveCandidate, error) {
	return nil, nil
}
func (startupPlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return nil, nil
}

func TestStartupWithoutOpenCode(t *testing.T) {
	if os.Getenv("OCMAN_TEST_NO_OPENCODE") != "" {
		host := hostlocal.New(hostlocal.Deps{PlatformEnabled: func(string) bool { return false }})
		if err := host.StopProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: "/repo"}); err != nil {
			t.Fatal(err)
		}
		if _, err := host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: "/repo"}); err == nil {
			t.Fatal("disabled restart accepted")
		}
		if _, err := host.ManagedOpencodes(t.Context()); err != nil {
			t.Fatal(err)
		}
		knownPlatforms["test-provider"] = func(context.Context, *db.DB, *state.DB, ocapi.Auth) platforms.Platform { return startupPlatform{} }
		flag.CommandLine = flag.NewFlagSet("ocman", flag.ExitOnError)
		os.Args = []string{"ocman", "-platforms=test-provider", "-addr=127.0.0.1:0", "-mcp-addr=", "-log-file=off"}
		time.AfterFunc(time.Second, func() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) })
		Run("dev", skillCatalog)
		return
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "opencode-called")
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\n: > \"$OCMAN_TEST_EXEC_MARKER\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStartupWithoutOpenCode$")
	cmd.Env = append(os.Environ(), "OCMAN_TEST_NO_OPENCODE=1", "OCMAN_TEST_EXEC_MARKER="+marker,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+dir,
		"XDG_DATA_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_STATE_HOME="+dir, "SHELL=/bin/sh",
		"OCMAN_AUTH_PASSWORD=", "OPENCODE_SERVER_PASSWORD=", "OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR=", "OCMAN_RELAY_URL=", "WAILS_BINDINGS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("startup: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "server stopped gracefully") {
		t.Fatalf("startup did not complete: %s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("disabled startup executed opencode: %v\n%s", err, out)
	}
}
