package local

import (
	"context"
	"os"
	"os/exec"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
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
