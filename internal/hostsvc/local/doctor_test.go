package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestDoctorReportsMixedResults(t *testing.T) {
	origLook, origWhisper := lookPath, whisperStatus
	t.Cleanup(func() { lookPath, whisperStatus = origLook, origWhisper })
	lookPath = func(bin string) (string, error) {
		if bin == "tmux" {
			return "", errors.New("not found")
		}
		return "/bin/" + bin, nil
	}
	whisperStatus = func() (string, string, string) { return "/bin/whisper", "", "" }

	dir := t.TempDir()
	checks := (&Host{deps: Deps{StateDir: dir}}).Doctor(context.Background())
	byID := map[string]bool{}
	for _, c := range checks {
		byID[c.ID] = c.OK
		if !c.OK && c.Hint == "" {
			t.Errorf("%s failed without hint", c.ID)
		}
	}
	for id, ok := range map[string]bool{"opencode": true, "tmux": false, "git": true, "whisper": false, "state-dir": true} {
		if got, seen := byID[id]; !seen || got != ok {
			t.Errorf("%s ok=%v seen=%v, want %v", id, got, seen, ok)
		}
	}

	if c := stateDirCheck(filepath.Join(dir, "missing")); c.OK {
		t.Errorf("missing state dir reported ok: %+v", c)
	}
	whisperStatus = func() (string, string, string) { return "", "", "" }
	if c := whisperCheck(); c.OK || c.Required {
		t.Errorf("whisper = %+v", c)
	}
	whisperStatus = func() (string, string, string) { return "/w", "/m", "" }
	if c := whisperCheck(); !c.OK || c.Hint == "" {
		t.Errorf("whisper without ffmpeg = %+v", c)
	}
	whisperStatus = func() (string, string, string) { return "/w", "/m", "/f" }
	if c := whisperCheck(); !c.OK {
		t.Errorf("whisper = %+v", c)
	}
}
