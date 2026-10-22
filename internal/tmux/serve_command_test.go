package tmux

import "testing"

func TestOpencodeServeCommandForPort(t *testing.T) {
	got := OpencodeServeCommandForPort(4242)
	if got != "exec env NODE_NO_WARNINGS=1 opencode serve --port 4242" {
		t.Fatalf("OpencodeServeCommandForPort(4242) = %q", got)
	}
}
