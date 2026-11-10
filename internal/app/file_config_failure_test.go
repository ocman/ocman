package app

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/testutil"
)

func TestConfigStartupFailureUsesDesktopDiagnostics(t *testing.T) {
	if os.Getenv("OCMAN_CONFIG_FAILURE_CHILD") == "1" {
		flag.CommandLine = flag.NewFlagSet("ocman", flag.ExitOnError)
		os.Args = []string{"ocman", "-gui", "-config=" + os.Getenv("OCMAN_CONFIG_FAILURE_PATH"), "-log-file=" + os.Getenv("OCMAN_CONFIG_FAILURE_LOG")}
		Run("test", skillCatalog)
		return
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("desktop dialogs are available on macOS and Linux")
	}
	for _, tc := range []struct{ name, config, message string }{
		{"invalid YAML", "version: [", "Failed to load configuration"},
		{"unavailable secret", "version: 1\nstartup:\n  auth-password: secret://env/OCMAN_CONFIG_FAILURE_MISSING\n", "Failed to load authentication configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path, logPath, marker := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "ocman.log"), filepath.Join(dir, "dialog")
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"osascript", "zenity"} {
				testutil.WriteScript(t, filepath.Join(bin, name), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OCMAN_CONFIG_FAILURE_DIALOG\"\n")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigStartupFailureUsesDesktopDiagnostics$")
			cmd.Env = testutil.IsolatedEnv("HOME="+dir, "PATH="+bin+":/usr/bin:/bin", "SHELL=/bin/sh",
				"OCMAN_CONFIG_FAILURE_CHILD=1", "OCMAN_CONFIG_FAILURE_PATH="+path, "OCMAN_CONFIG_FAILURE_LOG="+logPath,
				"OCMAN_CONFIG_FAILURE_DIALOG="+marker, "OCMAN_CONFIG_FAILURE_MISSING=", "OCMAN_AUTH_PASSWORD=", "WAILS_BINDINGS=")
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("startup exit = %v: %s", err, output)
			}
			for _, file := range []string{logPath, marker} {
				contents, err := os.ReadFile(file)
				if err != nil || !strings.Contains(string(contents), tc.message) {
					t.Errorf("startup failure missing from %s: %v", filepath.Base(file), err)
				}
			}
		})
	}
}
