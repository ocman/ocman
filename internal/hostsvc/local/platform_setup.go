package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
)

const adapterPackage = "@agentclientprotocol/codex-acp@2.5.0"

var setupInstallMu sync.Mutex

func (h *Host) CheckPlatformSetup(ctx context.Context, id string) (hostsvc.PlatformSetup, error) {
	if id != "codex" {
		return hostsvc.PlatformSetup{}, fmt.Errorf("unsupported platform setup %q", id)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	codex := os.Getenv("CODEX_PATH")
	if codex == "" {
		codex = "codex"
	}
	checks := []hostsvc.DoctorCheck{
		binCheck(codex, "Codex CLI", true, "Install the Codex CLI and sign in with codex login."),
		binCheck("node", "Node.js 22+", true, "Install Node.js 22 or newer."),
		binCheck("codex-acp", "Codex ACP adapter", true, "Install the adapter on this machine and ensure PNPM_HOME is on Ocman's PATH."),
		binCheck("pnpm", "pnpm installer", false, "Install pnpm to use the Install button, or install the adapter manually."),
	}
	if checks[1].OK {
		out, err := exec.CommandContext(ctx, checks[1].Detail, "--version").Output()
		major, parseErr := strconv.Atoi(strings.Split(strings.TrimPrefix(strings.TrimSpace(string(out)), "v"), ".")[0])
		checks[1].OK = err == nil && parseErr == nil && major >= 22
		if !checks[1].OK {
			checks[1].Detail = "Node.js 22 or newer is required"
			checks[1].Hint = "Upgrade Node.js on the machine running Ocman."
		}
	}
	return hostsvc.PlatformSetup{Ready: checks[0].OK && checks[1].OK && checks[2].OK,
		Installable: checks[0].OK && checks[1].OK && checks[3].OK,
		Command:     "pnpm add -g " + adapterPackage, Checks: checks}, nil
}

func (h *Host) InstallPlatformSetup(ctx context.Context, id string) (hostsvc.PlatformSetup, error) {
	if !setupInstallMu.TryLock() {
		return hostsvc.PlatformSetup{}, fmt.Errorf("an adapter installation is already running")
	}
	defer setupInstallMu.Unlock()
	status, err := h.CheckPlatformSetup(ctx, id)
	if err != nil || status.Ready {
		return status, err
	}
	if !status.Installable {
		return status, fmt.Errorf("install the Codex CLI, Node.js 22+ and pnpm before installing the adapter")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, status.Checks[3].Detail, "add", "-g", adapterPackage)
	cmd.WaitDelay = time.Second
	output := &setupOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return status, fmt.Errorf("adapter installation failed: %w: %s", err, output.text)
	}
	return h.CheckPlatformSetup(ctx, id)
}

// Keep package-manager errors useful without retaining unbounded output.
type setupOutput struct{ text string }

func (b *setupOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 8192 - len(b.text); remaining > 0 {
		b.text += string(p[:min(remaining, n)])
	}
	return n, nil
}
