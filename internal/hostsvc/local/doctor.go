package local

import (
	"context"
	"os"
	"os/exec"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

var lookPath = exec.LookPath

// Doctor reports this machine's prerequisite checks, in display order.
func (h *Host) Doctor(context.Context) []hostsvc.DoctorCheck {
	checks := []hostsvc.DoctorCheck{
		binCheck("tmux", "tmux", false, "brew install tmux"),
		binCheck("git", "git", true, "brew install git"),
	}
	if h.deps.PlatformEnabled == nil || h.deps.PlatformEnabled(string(platforms.OpenCodeID)) {
		checks = append([]hostsvc.DoctorCheck{binCheck(string(platforms.OpenCodeID), "OpenCode", true, "Install OpenCode: https://opencode.ai")}, checks...)
	}
	if h.deps.PlatformEnabled != nil && h.deps.PlatformEnabled("codex") {
		checks = append(checks,
			binCheck("codex", "Codex CLI", true, "Install Codex CLI, then run codex login: https://developers.openai.com/codex/cli"),
			binCheck("node", "Node.js for Codex ACP", true, "Install Node.js 22 or newer: https://nodejs.org"),
			binCheck("codex-acp", "Codex ACP adapter", true, "With Node.js 22+ installed, run pnpm add -g @agentclientprotocol/codex-acp@2.5.0. If pnpm has no global bin directory, run pnpm setup and reopen your terminal. Ensure PNPM_HOME is on Ocman's PATH, then re-check."))
	}
	if h.deps.StateDir != "" {
		checks = append(checks, stateDirCheck(h.deps.StateDir))
	}
	return checks
}

func binCheck(bin, label string, required bool, hint string) hostsvc.DoctorCheck {
	c := hostsvc.DoctorCheck{ID: bin, Label: label, Required: required}
	if path, err := lookPath(bin); err == nil {
		c.OK, c.Detail = true, path
	} else {
		c.Detail, c.Hint = bin+" not found on PATH", hint
	}
	return c
}

func stateDirCheck(dir string) hostsvc.DoctorCheck {
	c := hostsvc.DoctorCheck{ID: "state-dir", Label: "State directory", Required: true, Detail: dir}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		c.Detail, c.Hint = err.Error(), "Make "+dir+" writable by your user"
		return c
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	c.OK = true
	return c
}
