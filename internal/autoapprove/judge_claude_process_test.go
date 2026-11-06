package autoapprove

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// Admission time is not pipe-drain time. Exercise a slow slot followed by
// WaitDelay entirely in virtual time, including the slot's release ordering.
func TestClaudeJudgeWaitDelayReleasesHelperSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		taken, released := 0, 0
		var slotContext context.Context
		started := make(chan struct{})
		drain := make(chan struct{})
		judge := ClaudeJudge{CLI: "unused", Slot: func(ctx context.Context) (func(), error) {
			slotContext = ctx
			time.Sleep(9 * time.Second)
			taken++
			return func() { released++ }, nil
		}, output: func(cmd *exec.Cmd) ([]byte, error) {
			if cmd.WaitDelay != 2*time.Second {
				t.Errorf("WaitDelay = %s, want 2s", cmd.WaitDelay)
			}
			deadline, ok := slotContext.Deadline()
			if !ok || time.Until(deadline) != judgeTimeout-9*time.Second {
				t.Error("slot acquisition did not share the judge timeout")
			}
			close(started)
			<-drain
			time.Sleep(cmd.WaitDelay)
			// Even a complete safe verdict is untrusted when output fails.
			return []byte(`{"verdict":"safe","reasoning":"ok"}`), exec.ErrWaitDelay
		}}
		done := make(chan JudgeResult, 1)
		go func() { done <- judge.JudgeWithCallback(t.Context(), "", "bash", nil, nil, nil, nil) }()
		<-started
		synctest.Wait()
		if taken != 1 || released != 0 {
			t.Errorf("before pipe drain: taken=%d released=%d", taken, released)
		}
		select {
		case <-done:
			t.Fatal("judge returned before pipe drain finished")
		default:
		}
		close(drain)
		r := <-done
		if r.Verdict != verdictUnsafe || !r.EvaluationFailed || released != 1 {
			t.Fatalf("result=%+v released=%d", r, released)
		}
	})
}

// Keep a real inherited stdout descriptor open until the test releases it,
// rather than racing a sleeping grandchild against a wall-clock assertion.
func TestClaudeJudgeDoesNotWaitForLeakedStdout(t *testing.T) {
	controlR, controlW := judgeTestPipe(t)
	doneR, doneW := judgeTestPipe(t)
	cli := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat > /dev/null\n(cat <&3; printf done >&4) &\nprintf '%s' '{\"verdict\":\"safe\",\"reasoning\":\"ok\"}'\n"
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// This is a deadlock guard, not an elapsed-time assertion. Normal execution
	// waits only a nanosecond after process exit, regardless of machine load.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	released := 0
	judge := ClaudeJudge{CLI: cli, Slot: func(context.Context) (func(), error) {
		return func() { released++ }, nil
	}, output: func(cmd *exec.Cmd) ([]byte, error) {
		if cmd.WaitDelay != 2*time.Second {
			t.Errorf("WaitDelay = %s, want 2s", cmd.WaitDelay)
		}
		cmd.WaitDelay = time.Nanosecond
		cmd.ExtraFiles = []*os.File{controlR, doneW}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		t.Cleanup(func() {
			// WaitDelay closes pipes; it does not terminate descendants. Kill
			// only this fake CLI's private group, including on assertion failure.
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		})
		out, err := cmd.Output()
		if !errors.Is(err, exec.ErrWaitDelay) {
			t.Errorf("Output error = %v, want ErrWaitDelay", err)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
			t.Errorf("fake CLI was not successfully reaped: %v", cmd.ProcessState)
		}
		return out, err
	}}
	r := judge.JudgeWithCallback(ctx, "", "bash", nil, nil, nil, nil)
	if r.Verdict != verdictUnsafe || !r.EvaluationFailed || released != 1 {
		t.Fatalf("result=%+v released=%d", r, released)
	}
	// The descendant cannot finish until this EOF, which is sent only after
	// the judge returned and released its helper slot.
	_ = controlR.Close()
	_ = doneW.Close()
	_ = controlW.Close()
	if err := doneR.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// EOF also proves every descendant closed its completion descriptor.
	done, err := io.ReadAll(doneR)
	if err != nil || string(done) != "done" {
		t.Fatalf("descendant cleanup = %q, %v", done, err)
	}
}

func judgeTestPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}
