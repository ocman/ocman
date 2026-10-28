package app

import (
	"context"
	"flag"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	hostlocal "github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/state"
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
		knownPlatforms["test-provider"] = func(context.Context, *db.DB, *state.DB, ocapi.Auth) platforms.Integration { return startupPlatform{} }
		flag.CommandLine = flag.NewFlagSet("ocman", flag.ExitOnError)
		addr := os.Getenv("OCMAN_TEST_STARTUP_ADDR")
		os.Args = []string{"ocman", "-platforms=test-provider", "-addr=" + addr, "-mcp-addr=", "-log-file=off"}
		if os.Getenv("OCMAN_TEST_NO_OPENCODE") == "saved" {
			store, err := state.Open(state.DefaultDBPath())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetSetting(t.Context(), "platforms.enabled", "claudecode"); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			knownPlatforms["claudecode"] = knownPlatforms["test-provider"]
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
		go func() {
			client := &http.Client{Timeout: 100 * time.Millisecond}
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if resp, err := client.Get("http://" + addr + "/api/auth/me"); err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
						return
					}
				}
			}
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		}()
		Run("dev", skillCatalog)
		return
	}
	for _, mode := range []string{"explicit", "saved"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			_ = listener.Close()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "opencode-called")
			if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\n: > \"$OCMAN_TEST_EXEC_MARKER\"\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStartupWithoutOpenCode$")
			cmd.Env = append(os.Environ(), "OCMAN_TEST_NO_OPENCODE="+mode, "OCMAN_TEST_EXEC_MARKER="+marker, "OCMAN_TEST_STARTUP_ADDR="+addr,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+dir,
				"XDG_DATA_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_STATE_HOME="+dir, "SHELL=/bin/sh",
				"OCMAN_AUTH_PASSWORD=", "OPENCODE_SERVER_PASSWORD=", "OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR=", "OCMAN_RELAY_URL=", "OCMAN_PUBLIC_BASE_URL=", "WAILS_BINDINGS=")
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
			if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
				if err == nil && entry.Name() == "analytics-cache.db" {
					t.Errorf("canonical startup created obsolete analytics mirror: %s", path)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
