package platforms

import "testing"

func TestDisplayName(t *testing.T) {
	for id, want := range map[ID]string{OpenCodeID: "OpenCode", "claudecode": "Claude Code", "codex": "codex"} {
		if got := DisplayName(id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
}
