package local

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/whisper"
)

// lookPath and whisperStatus are seams for tests.
var (
	lookPath      = exec.LookPath
	whisperStatus = whisper.Status
)

// Doctor reports this machine's prerequisite checks, in display order.
func (h *Host) Doctor(context.Context) []hostsvc.DoctorCheck {
	checks := []hostsvc.DoctorCheck{
		binCheck("opencode", "OpenCode", true, "Install OpenCode: https://opencode.ai"),
		binCheck("tmux", "tmux", false, "brew install tmux"),
		binCheck("git", "git", true, "brew install git"),
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		checks = append(checks, binCheck("lsof", "lsof", false, "Install lsof; discovering externally started OpenCode instances needs it"))
	}
	checks = append(checks, whisperCheck())
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

func whisperCheck() hostsvc.DoctorCheck {
	c := hostsvc.DoctorCheck{ID: "whisper", Label: "Voice transcription (whisper)"}
	binary, model, ffmpeg := whisperStatus()
	switch {
	case binary == "":
		c.Detail, c.Hint = "whisper-cpp not found on PATH", "brew install whisper-cpp"
	case model == "":
		c.Detail, c.Hint = "whisper model not found", "Download a ggml model (e.g. ggml-base.en.bin) next to whisper-cpp's models directory"
	case ffmpeg == "":
		c.OK, c.Detail, c.Hint = true, binary+" (ffmpeg missing: only wav/mp3/ogg/flac)", "brew install ffmpeg"
	default:
		c.OK, c.Detail = true, binary
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
