package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupBins(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("CODEX_PATH", "")
	for name, script := range map[string]string{"codex": "exit 0", "node": "echo " + version, "pnpm": "exit 0"} {
		writeSetupBin(t, dir, name, script)
	}
	return dir
}

func writeSetupBin(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformSetup(t *testing.T) {
	for _, version := range []string{"v22.0.0", "v25.0.0", "v20.1.0", "unknown"} {
		t.Run(version, func(t *testing.T) {
			dir := setupBins(t, version)
			h := New(Deps{})
			status, err := h.CheckPlatformSetup(t.Context(), "codex")
			valid := version == "v22.0.0" || version == "v25.0.0"
			if err != nil || status.Ready || status.Installable != valid {
				t.Fatalf("status=%+v err=%v", status, err)
			}
			writeSetupBin(t, dir, "codex-acp", "exit 0")
			status, err = h.CheckPlatformSetup(t.Context(), "codex")
			if err != nil || status.Ready != valid {
				t.Fatalf("status=%+v err=%v", status, err)
			}
			if err := os.Remove(filepath.Join(dir, "pnpm")); err != nil {
				t.Fatal(err)
			}
			status, _ = h.CheckPlatformSetup(t.Context(), "codex")
			if status.Ready != valid || status.Installable {
				t.Fatalf("pnpm should not be required for execution: %+v", status)
			}
		})
	}
}

func TestPlatformSetupInstall(t *testing.T) {
	dir := setupBins(t, "v22.0.0")
	args := filepath.Join(dir, "args")
	writeSetupBin(t, dir, "pnpm", "printf '%s\\n' \"$@\" > '"+args+"'\nprintf '#!/bin/sh\\nexit 0\\n' > '"+filepath.Join(dir, "codex-acp")+"'\n/bin/chmod +x '"+filepath.Join(dir, "codex-acp")+"'")
	h := New(Deps{})
	status, err := h.InstallPlatformSetup(t.Context(), "codex")
	if err != nil || !status.Ready {
		t.Fatalf("%+v %v", status, err)
	}
	got, err := os.ReadFile(args)
	if err != nil || string(got) != "add\n-g\n"+adapterPackage+"\n" {
		t.Fatalf("args=%s err=%v", got, err)
	}
	if _, err := h.InstallPlatformSetup(t.Context(), "codex"); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformSetupInstallFailures(t *testing.T) {
	dir := setupBins(t, "v22.0.0")
	h := New(Deps{})
	if _, err := h.CheckPlatformSetup(t.Context(), "other"); err == nil {
		t.Fatal("accepted unknown platform")
	}
	if _, err := h.InstallPlatformSetup(t.Context(), "other"); err == nil {
		t.Fatal("installed unknown platform")
	}
	setupInstallMu.Lock()
	_, err := h.InstallPlatformSetup(t.Context(), "codex")
	setupInstallMu.Unlock()
	if err == nil {
		t.Fatal("accepted concurrent installation")
	}
	writeSetupBin(t, dir, "pnpm", "echo 'global bin missing' >&2\nexit 1")
	if _, err := h.InstallPlatformSetup(t.Context(), "codex"); err == nil || !strings.Contains(err.Error(), "global bin missing") {
		t.Fatalf("%v", err)
	}
	t.Setenv("CODEX_PATH", filepath.Join(dir, "missing"))
	if _, err := h.InstallPlatformSetup(t.Context(), "codex"); err == nil {
		t.Fatal("installed without prerequisites")
	}
	b := &setupOutput{}
	n, err := b.Write([]byte(strings.Repeat("x", 9000)))
	if err != nil || n != 9000 || len(b.text) != 8192 {
		t.Fatalf("n=%d len=%d err=%v", n, len(b.text), err)
	}
	_, _ = b.Write([]byte("ignored"))
	if len(b.text) != 8192 {
		t.Fatal("output exceeded limit")
	}
}
