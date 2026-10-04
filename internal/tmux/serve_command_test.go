package tmux

import "testing"

func TestOpencodeServeCommandForPort(t *testing.T) {
	got := OpencodeServeCommandForPort(4242)
	if got != "exec env NODE_NO_WARNINGS=1 opencode serve --port 4242" {
		t.Fatalf("OpencodeServeCommandForPort(4242) = %q", got)
	}
	if !WindowRunsOpencode(Window{Command: "node", StartCommand: got}) {
		t.Fatal("serve pane not recognised as running opencode")
	}
}
