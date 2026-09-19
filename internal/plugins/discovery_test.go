package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDescribeHelper runs in a separate process with the launcher's real env.
func TestDescribeHelper(t *testing.T) {
	if len(os.Args) < 5 || os.Args[2] != "--" {
		return
	}
	if os.Args[4] != "describe" || os.Getenv("DISCOVERY_SECRET") != "" || os.Getenv("HOME") != "" {
		os.Exit(2)
	}
	cwd, _ := os.Getwd()
	if cwd != "/" || !validToken(os.Getenv("OCMAN_PLUGIN_TOKEN")) {
		os.Exit(2)
	}
	e := hello(ModeDescribe)
	e.Hello.Token = os.Getenv("OCMAN_PLUGIN_TOKEN")
	switch os.Args[3] {
	case "timeout":
		time.Sleep(10 * time.Second)
	case "malformed":
		fmt.Println("not json")
		os.Exit(0)
	case "overflow":
		fmt.Print(strings.Repeat("x", MaxMessageBytes+3))
		os.Exit(0)
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("secret", MaxDescribeStderrBytes))
		os.Exit(0)
	case "exit":
		fmt.Fprint(os.Stderr, "sensitive diagnostics")
		os.Exit(1)
	case "identity":
		e.Hello.Description.ID = "bad"
	case "changed-id":
		e.Hello.Description.ID = "org.example.changed"
	case "token":
		e.Hello.Token = testToken
	case "version":
		e.Hello.Description.Protocol.Major++
	case "scope":
		e.Hello.Description.Scope = "unknown"
	case "capability":
		e.Hello.Description.Capabilities[0].Version.Major = 0
	case "grants":
		e.Hello.Description.RequestedGrants = []string{"read", "read"}
	case "settings":
		e.Hello.Description.Settings = []Setting{{Key: "token", Type: "number", Secret: true}}
	case "mutate":
		f, err := os.OpenFile(os.Args[5], os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			os.Exit(2)
		}
		_, _ = f.WriteString("\n# changed\n")
		_ = f.Close()
	}
	_ = json.NewEncoder(os.Stdout).Encode(e)
	if os.Args[3] == "extra" {
		_ = json.NewEncoder(os.Stdout).Encode(e)
	}
	os.Exit(0)
}

var warmDescribeHelper sync.Once
var warmDescribeHelperErr error

func helperExecutable(t *testing.T, dir, name, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// macOS can spend seconds validating a newly linked test binary on its
	// first execution. Do that in fixture setup, outside describe's deadline.
	warmDescribeHelper.Do(func() {
		warmDescribeHelperErr = exec.Command(exe, "-test.run=^$").Run()
	})
	if warmDescribeHelperErr != nil {
		t.Fatal(warmDescribeHelperErr)
	}
	path := filepath.Join(dir, name)
	// Race-instrumented helpers otherwise spend one second sleeping on exit.
	script := fmt.Sprintf("#!/bin/sh\nexport GORACE=atexit_sleep_ms=0\nexec %q -test.run=^TestDescribeHelper$ -- %q \"$@\" %q\n", exe, mode, path)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanDescribe(t *testing.T) {
	t.Setenv("DISCOVERY_SECRET", "must not escape")
	for _, tt := range []struct {
		mode string
		want error
	}{
		{"success", nil}, {"timeout", context.DeadlineExceeded}, {"malformed", ErrInvalidMessage},
		{"overflow", ErrMessageTooLarge}, {"stderr", ErrMessageTooLarge}, {"exit", ErrDescribe},
		{"identity", ErrInvalidMessage}, {"token", ErrHandshake}, {"version", ErrIncompatibleVersion},
		{"scope", ErrInvalidMessage}, {"capability", ErrInvalidMessage}, {"grants", ErrInvalidMessage},
		{"settings", ErrInvalidMessage}, {"extra", ErrInvalidMessage}, {"mutate", ErrExecutableChanged},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			dir := t.TempDir()
			helperExecutable(t, dir, "ocman-plugin-test", tt.mode)
			start := time.Now()
			got, err := Scan(context.Background(), dir, nil)
			if err != nil || len(got) != 1 {
				t.Fatalf("scan: %v, %v", got, err)
			}
			if !errors.Is(got[0].Err, tt.want) {
				t.Fatalf("got %v, want %v", got[0].Err, tt.want)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("describe exceeded deadline")
			}
			if tt.want == nil && (got[0].Description.ID != "org.example.test" || len(got[0].Checksum) != 64) {
				t.Fatalf("bad result: %+v", got[0])
			}
		})
	}
}

func TestScanFilteringAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	first := helperExecutable(t, dir, "ocman-plugin-a", "success")
	helperExecutable(t, dir, ".ocman-plugin-hidden", "exit")
	helperExecutable(t, dir, "unrelated", "exit")
	helperExecutable(t, dir, "ocman-plugin-", "exit")
	nonexec := helperExecutable(t, dir, "ocman-plugin-nonexec", "exit")
	if err := os.Chmod(nonexec, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, filepath.Join(dir, "ocman-plugin-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "ocman-plugin-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	helperExecutable(t, filepath.Join(dir, "ocman-plugin-dir"), "ocman-plugin-nested", "exit")
	got, err := Scan(context.Background(), dir, nil)
	if err != nil || len(got) != 1 || got[0].Err != nil {
		t.Fatalf("filtered: %+v, %v", got, err)
	}
	helperExecutable(t, dir, "ocman-plugin-b", "success")
	got, err = Scan(context.Background(), dir, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("duplicates: %+v, %v", got, err)
	}
	for _, candidate := range got {
		if !errors.Is(candidate.Err, ErrDuplicateID) {
			t.Fatalf("not conflicted: %+v", candidate)
		}
	}
}

func TestScanRescanIdentityAndChecksum(t *testing.T) {
	dir := t.TempDir()
	path := helperExecutable(t, dir, "ocman-plugin-test", "success")
	first, err := Scan(context.Background(), dir, nil)
	if err != nil || len(first) != 1 || first[0].Err != nil {
		t.Fatalf("first: %+v %v", first, err)
	}
	known := map[string]string{path: first[0].Description.ID}
	helperExecutable(t, dir, "ocman-plugin-test", "changed-id")
	second, _ := Scan(context.Background(), dir, known)
	if len(second) != 1 || !errors.Is(second[0].Err, ErrIdentityChanged) {
		t.Fatalf("identity: %+v", second)
	}
	helperExecutable(t, dir, "ocman-plugin-test", "success")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("# new release\n")
	_ = f.Close()
	third, _ := Scan(context.Background(), dir, known)
	if len(third) != 1 || third[0].Err != nil || third[0].Checksum == first[0].Checksum {
		t.Fatalf("checksum: %+v", third)
	}
}

func TestDiscoveryDirectory(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", "")
	t.Setenv("HOME", t.TempDir())
	dir, err := DiscoveryDirectory()
	if err != nil || dir != filepath.Join(os.Getenv("HOME"), ".local/share/ocman/plugins") {
		t.Fatalf("default: %s %v", dir, err)
	}
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	dir, err = DiscoveryDirectory()
	if err != nil || dir != os.Getenv("OCMAN_PLUGIN_DIR") {
		t.Fatalf("override: %s %v", dir, err)
	}
	if got, err := Scan(context.Background(), filepath.Join(dir, "missing"), nil); err != nil || len(got) != 0 {
		t.Fatalf("missing: %+v %v", got, err)
	}
	path := helperExecutable(t, dir, "file", "success")
	if _, err := Scan(context.Background(), path, nil); err == nil {
		t.Fatal("accepted non-directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	helperExecutable(t, dir, "ocman-plugin-test", "success")
	if _, err := Scan(ctx, dir, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	t.Setenv("OCMAN_PLUGIN_DIR", "")
	t.Setenv("HOME", "")
	if _, err := DiscoveryDirectory(); err == nil {
		t.Fatal("missing home accepted")
	}
}
