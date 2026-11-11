package gitexec

import (
	"testing"
	"time"
)

func TestWithWaitDelay(t *testing.T) {
	cmd := Command(t.Context(), "status")
	if cmd.cmd.WaitDelay != 0 {
		t.Fatal("unrelated Git callers must retain their default wait behavior")
	}
	if got := cmd.WithWaitDelay(100 * time.Millisecond); got != cmd || cmd.cmd.WaitDelay != 100*time.Millisecond {
		t.Fatal("WithWaitDelay did not configure and return the command")
	}
}
