package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// InstallProviderTmux installs a fake tmux whose detached providers each own a
// process group. Cleanup stops descendants even if their supervisor crashed.
func InstallProviderTmux(t testing.TB, bin, pids string) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	fixtures := filepath.Join(filepath.Dir(source), "testdata")
	groupLauncherOnce.Do(func() {
		dir, err := os.MkdirTemp("", "provider-group-build-")
		if err != nil {
			groupLauncherErr = err
			return
		}
		defer os.RemoveAll(dir)
		binary := filepath.Join(dir, "process-group")
		cmd := exec.Command("go", "build", "-o", binary, filepath.Join(fixtures, "processgroup"))
		cmd.Env = groupLauncherEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			groupLauncherErr = fmt.Errorf("build process-group launcher: %w\n%s", err, out)
			return
		}
		groupLauncher, groupLauncherErr = os.ReadFile(binary)
	})
	if groupLauncherErr != nil {
		t.Fatal(groupLauncherErr)
	}
	for _, dir := range []string{bin, pids} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(bin, "process-group")
	if err := os.WriteFile(launcher, groupLauncher, 0o700); err != nil {
		t.Fatal(err)
	}
	WarmExecutable(t, launcher)
	script, err := os.ReadFile(filepath.Join(fixtures, "provider-tmux"))
	if err != nil {
		t.Fatal(err)
	}
	WriteScript(t, filepath.Join(bin, "tmux"), string(script))
	t.Cleanup(func() {
		paths, err := filepath.Glob(filepath.Join(pids, "*.pid"))
		if err != nil {
			t.Error(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Error(err)
				continue
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Errorf("invalid provider PID in %s: %v", path, err)
				continue
			}
			if err := StopProcessGroup(pid); err != nil {
				t.Errorf("stop test provider %s: %v", path, err)
			}
		}
	})
}

// StopProcessGroup stops only a private test process group, with bounded
// escalation. Zombies count as exited: container PID 1 may not reap orphans.
func StopProcessGroup(pid int) error {
	if pid <= 1 || pid == syscall.Getpgrp() {
		return fmt.Errorf("refusing unsafe process group %d", pid)
	}
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if err := syscall.Kill(-pid, signal); errors.Is(err, syscall.ESRCH) {
			return nil
		} else if err != nil {
			// macOS can return EPERM for a group containing only zombies.
			if running, probeErr := processGroupRunning(pid); probeErr == nil && !running {
				return nil
			}
			return err
		}
		deadline := time.Now().Add(time.Second)
		for {
			running, err := processGroupRunning(pid)
			if err != nil {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				return err
			}
			if !running {
				return nil
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return fmt.Errorf("process group %d did not exit", pid)
}

func processGroupRunning(pid int) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pgid=,stat=").Output()
	if err != nil {
		return false, fmt.Errorf("checking test process group: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == strconv.Itoa(pid) && !strings.HasPrefix(fields[1], "Z") {
			return true, nil
		}
	}
	return false, nil
}

var (
	groupLauncherOnce sync.Once
	groupLauncher     []byte
	groupLauncherErr  error
	groupLauncherEnv  = os.Environ() // capture before a test changes HOME or PATH
)
