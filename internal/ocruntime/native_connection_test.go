package ocruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func nativeAccountConfig(t *testing.T) NativeConnectionConfig {
	t.Helper()
	root := t.TempDir()
	return NativeConnectionConfig{
		DBPath: filepath.Join(root, "native.db"), ConfigDir: filepath.Join(root, "config"),
		DataDir: filepath.Join(root, "data"), StateDir: filepath.Join(root, "state"),
		CacheDir: filepath.Join(root, "cache"), RuntimeDir: filepath.Join(root, "runtime"), Executable: "/usr/bin/env",
	}
}

func TestNativeConnectionEnvironmentIsAccountLocal(t *testing.T) {
	t.Setenv("OPENCODE_DB", "/never/open/shared.db")
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"providers":{"secret":{}}}`)
	t.Setenv("ANTHROPIC_API_KEY", "parent-secret")
	home := os.Getenv("HOME")
	for _, password := range []string{"first-password", "second-password"} {
		cfg := nativeAccountConfig(t)
		rt, err := NewNativeConnectionRuntime(cfg, ocapi.New(password))
		if err != nil {
			t.Fatal(err)
		}
		rt.launch = func(_ context.Context, directory, command string, env map[string]string) (string, error) {
			if env["OPENCODE_DB"] != cfg.DBPath || env["OPENCODE_PASSWORD"] != password || env["HOME"] != home {
				t.Fatal("runtime did not preserve explicit account database, auth and real HOME")
			}
			for _, key := range []string{"ANTHROPIC_API_KEY", "OPENCODE_CONFIG_CONTENT", "OPENCODE_SERVER", "AWS_PROFILE"} {
				if _, found := env[key]; found {
					t.Fatalf("inherited override %s", key)
				}
			}
			if strings.Contains(command, password) {
				t.Fatal("password embedded in command")
			}
			// Substitute a fake executable that prints its environment. An
			// injected tmux-server credential must disappear at the exec boundary.
			command = strings.TrimSuffix(command, " serve --port 8123")
			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", command)
			cmd.Env = []string{"ANTHROPIC_API_KEY=tmux-secret", "AWS_PROFILE=live", "OPENCODE_CONFIG_CONTENT=tmux-config"}
			for key, value := range env {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "tmux-secret") || strings.Contains(string(out), "AWS_PROFILE=") || strings.Contains(string(out), "OPENCODE_CONFIG_CONTENT=") {
				t.Fatalf("unsafe inherited environment: %s", out)
			}
			return directory, nil
		}
		if _, err := rt.Launch(t.Context(), LaunchSpec{RepoRoot: cfg.RuntimeDir, Port: 8123}); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("HOME") != home || os.Getenv("OPENCODE_DB") != "/never/open/shared.db" {
		t.Fatal("global environment mutated")
	}
}

func TestNativeConnectionRejectsIncompleteConfiguration(t *testing.T) {
	good := nativeAccountConfig(t)
	for _, mutate := range []func(*NativeConnectionConfig){
		func(c *NativeConnectionConfig) { c.DBPath = "" },
		func(c *NativeConnectionConfig) { c.ConfigDir = "relative" },
		func(c *NativeConnectionConfig) { c.DataDir = "" },
		func(c *NativeConnectionConfig) { c.StateDir = "" },
		func(c *NativeConnectionConfig) { c.CacheDir = "" },
		func(c *NativeConnectionConfig) { c.RuntimeDir = "" },
		func(c *NativeConnectionConfig) { c.Executable = "" },
	} {
		bad := good
		mutate(&bad)
		if _, err := NewNativeConnectionRuntime(bad, ocapi.New("test")); err == nil {
			t.Fatal("accepted incomplete configuration")
		}
	}
	if _, err := NewNativeConnectionRuntime(good, ocapi.New("")); err == nil {
		t.Fatal("accepted unauthenticated account runtime")
	}
}

func TestNativeConnectionInspectionClonesOnlyRuntimeDirectory(t *testing.T) {
	config := nativeAccountConfig(t)
	runtime, err := NewNativeConnectionRuntime(config, ocapi.New("private-password"))
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := t.TempDir()
	copyRuntime, err := runtime.ForInspection(privateRoot)
	if err != nil {
		t.Fatal(err)
	}
	copyConfig := copyRuntime.(*NativeRuntime).connection
	if copyConfig.RuntimeDir != privateRoot || runtime.connection.RuntimeDir != config.RuntimeDir || copyConfig.DBPath != config.DBPath || copyConfig.ConfigDir != config.ConfigDir || copyConfig.DataDir != config.DataDir || copyConfig.StateDir != config.StateDir {
		t.Fatal("inspection changed native account identity or mutated saved runtime")
	}
	if _, err := NewNativeRuntime().ForInspection(privateRoot); err == nil {
		t.Fatal("inspection accepted legacy runtime")
	}
}
