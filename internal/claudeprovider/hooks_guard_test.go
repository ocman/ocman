package claudeprovider

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedHooksSkipMissingBinaryOutsideOcman(t *testing.T) {
	for _, marker := range []string{"", "0"} {
		t.Run("marker="+marker, func(t *testing.T) {
			t.Setenv("OCMAN_MANAGED", marker)
			var config struct {
				Hooks map[string][]struct {
					Hooks []struct{ Command string }
				}
			}
			if err := json.Unmarshal(hooksJSON(HookCommand(filepath.Join(t.TempDir(), "missing ocman"), "127.0.0.1:1", t.TempDir())), &config); err != nil {
				t.Fatal(err)
			}
			for event, matchers := range config.Hooks {
				cmd := exec.Command("sh", "-c", matchers[0].Hooks[0].Command)
				cmd.Stdin = strings.NewReader(`{"session_id":"s1"}`)
				if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
					t.Errorf("%s: error = %v, output = %s", event, err, out)
				}
			}
		})
	}
}

func TestGeneratedHooksRunInsideOcman(t *testing.T) {
	t.Setenv("OCMAN_MANAGED", "1")
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	if err := json.Unmarshal(hooksJSON("printf '%s'"), &config); err != nil {
		t.Fatal(err)
	}
	for event, matchers := range config.Hooks {
		out, err := exec.Command("sh", "-c", matchers[0].Hooks[0].Command).CombinedOutput()
		if err != nil || string(out) != event {
			t.Errorf("%s: error = %v, output = %s", event, err, out)
		}
	}
}
