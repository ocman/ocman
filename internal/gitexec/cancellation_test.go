package gitexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/testutil"
)

func TestRunningCommandCancellationPreservesCause(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteScript(t, filepath.Join(dir, "git"), "#!/bin/sh\n[ -z \"$READY\" ] && exit 0\nprintf ready > \"$READY\"\nexec sleep 30\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, method := range []string{"output", "combined", "run", "stream"} {
		for _, deadline := range []bool{false, true} {
			t.Run(method+map[bool]string{false: "/cancel", true: "/deadline"}[deadline], func(t *testing.T) {
				ready := filepath.Join(t.TempDir(), "ready")
				ctx, cancel := context.WithCancel(t.Context())
				want := context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
					want = context.DeadlineExceeded
				}
				defer cancel()
				cmd := Command(ctx, "status").WithEnv("READY=" + ready)
				done := make(chan error, 1)
				go func() {
					var err error
					switch method {
					case "output":
						_, err = cmd.Output()
					case "combined":
						_, err = cmd.CombinedOutput()
					case "run":
						err = cmd.Run()
					case "stream":
						err = cmd.Stream(func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
					}
					done <- err
				}()
				wait := time.Now().Add(time.Second)
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					if time.Now().After(wait) {
						t.Fatal("fixture did not start before cancellation")
					}
					time.Sleep(time.Millisecond)
				}
				if !deadline {
					cancel()
				}
				err := <-done
				var exit *exec.ExitError
				if !errors.Is(err, want) || !errors.As(err, &exit) {
					t.Fatalf("got %v, want both %v and process exit evidence", err, want)
				}
			})
		}
	}
}

func TestUnrelatedKilledCommandIsNotCancellation(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteScript(t, filepath.Join(dir, "git"), "#!/bin/sh\nkill -KILL $$\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(t.Context())
	cmd := Command(ctx, "status")
	err := cmd.Run()
	cancel() // A later caller cancellation must not relabel the process failure.
	var exit *exec.ExitError
	if !errors.As(err, &exit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unrelated process kill misclassified: %v", err)
	}
}
