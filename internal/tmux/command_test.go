package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunCommandDeadlineStopsBlockingTmux(t *testing.T) {
	fakeTmux(t, "exec sleep 30")

	start := time.Now()
	err := runCommand(t.Context(), 20*time.Millisecond, "list-sessions")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runCommand error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("runCommand returned after %v, want under 1s", elapsed)
	}
}

func TestRunCommandCancellationTerminatesTmux(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	fakeTmux(t, "printf '%s' \"$$\" > \"$PID_FILE\"\nexec sleep 30")
	t.Setenv("PID_FILE", pidFile)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runCommand(ctx, 30*time.Second, "list-sessions")
	}()

	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("parse pid: %v", err)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("fake tmux did not start")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runCommand error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runCommand did not return after cancellation")
	}

	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("tmux process %d still exists after cancellation: %v", pid, err)
	}
}
