package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStopProcessGroupRejectsUnsafeIDs(t *testing.T) {
	for _, pid := range []int{-1, 0, 1, syscall.Getpgrp()} {
		if err := StopProcessGroup(pid); err == nil {
			t.Errorf("accepted unsafe group %d", pid)
		}
	}
}

func TestStopProcessGroupAcceptsExitedUnreapedChild(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })
	WaitFor(t, 5*time.Second, "unreaped child exited", func() bool {
		running, err := processGroupRunning(cmd.Process.Pid)
		return err == nil && !running
	})
	if err := StopProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("already-exited group: %v", err)
	}
}

func TestStopProcessGroupEscalates(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("/bin/sh", "-c", `trap '' TERM; printf ready > "$1"; while :; do sleep 1; done`, "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	WaitFor(t, 5*time.Second, "signal handler installed", func() bool {
		_, err := os.Stat(ready)
		return err == nil
	})
	if err := StopProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("signal-ignoring process exited successfully instead of being killed")
	}
	if running, err := processGroupRunning(cmd.Process.Pid); err != nil || running {
		t.Fatalf("group still executing: running=%v err=%v", running, err)
	}
	if err := StopProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("repeated stop: %v", err)
	}
}

func TestProcessGroupRunning(t *testing.T) {
	for _, status := range []string{"S", "Z", ""} {
		t.Run(status, func(t *testing.T) {
			bin := t.TempDir()
			WriteScript(t, filepath.Join(bin, "ps"), "#!/bin/sh\nprintf '%s\\n' '123 "+status+"' '456 S'\n")
			t.Setenv("PATH", bin)
			if running, err := processGroupRunning(123); err != nil || running != (status == "S") {
				t.Fatalf("status %q: running=%v err=%v", status, running, err)
			}
		})
	}
}

func TestProcessGroupProbeFailure(t *testing.T) {
	bin := t.TempDir()
	WriteScript(t, filepath.Join(bin, "ps"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", bin)
	if _, err := processGroupRunning(123); err == nil || !strings.Contains(err.Error(), "checking test process group") {
		t.Fatalf("probe failure: %v", err)
	}
}

func TestStopProcessGroupProbeFailureStillKills(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("/bin/sh", "-c", `trap '' TERM; printf ready > "$1"; exec sleep 30`, "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	WaitFor(t, 5*time.Second, "signal handler installed", func() bool {
		_, err := os.Stat(ready)
		return err == nil
	})
	bin := t.TempDir()
	WriteScript(t, filepath.Join(bin, "ps"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", bin)
	if err := StopProcessGroup(cmd.Process.Pid); err == nil {
		t.Fatal("process probe failure was hidden")
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("signal-ignoring process was not killed")
	}
}

func TestProviderLauncherRejectsUnsafeStop(t *testing.T) {
	dir := t.TempDir()
	InstallProviderTmux(t, dir, dir)
	for _, arg := range []string{"bad-pid", strconv.Itoa(syscall.Getpgrp())} {
		if out, err := exec.Command(filepath.Join(dir, "process-group"), "--stop", arg).CombinedOutput(); err == nil {
			t.Fatalf("accepted stop %q: %s", arg, out)
		}
	}
}
