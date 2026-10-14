package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestDoctorReportsMixedResults(t *testing.T) {
	origLook := lookPath
	t.Cleanup(func() { lookPath = origLook })
	lookPath = func(bin string) (string, error) {
		if bin == "tmux" {
			return "", errors.New("not found")
		}
		return "/bin/" + bin, nil
	}

	dir := t.TempDir()
	checks := (&Host{deps: Deps{StateDir: dir}}).Doctor(context.Background())
	byID := map[string]bool{}
	for _, c := range checks {
		byID[c.ID] = c.OK
		if !c.OK && c.Hint == "" {
			t.Errorf("%s failed without hint", c.ID)
		}
	}
	for id, ok := range map[string]bool{"opencode": true, "tmux": false, "git": true, "state-dir": true} {
		if got, seen := byID[id]; !seen || got != ok {
			t.Errorf("%s ok=%v seen=%v, want %v", id, got, seen, ok)
		}
	}

	if c := stateDirCheck(filepath.Join(dir, "missing")); c.OK {
		t.Errorf("missing state dir reported ok: %+v", c)
	}
}

func TestDoctorDoesNotAdvertiseOptionalFeatureTools(t *testing.T) {
	for _, check := range New(Deps{}).Doctor(t.Context()) {
		if check.ID == "lsof" || check.ID == "whisper" {
			t.Fatalf("feature-specific tool appears in global setup: %+v", check)
		}
	}
}
