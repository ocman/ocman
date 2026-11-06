package claudetest

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/testutil"
)

// A fixture gate deliberately ignores stdin EOF, as a stuck agent can. Killing
// only the supervisor leaves this child alive after its test directory is gone.
func TestFakeTmuxCleansUpBlockedChildren(t *testing.T) {
	for _, mode := range []string{"cleanup", "crashed supervisor", "same-name relaunch", "kill-session"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			childFile, life := filepath.Join(dir, "child"), filepath.Join(dir, "life")
			fixture := filepath.Join(dir, "fixture.jsonl")
			if err := os.WriteFile(fixture, []byte(`{"direction":"wait","message":"`+filepath.Join(dir, "blocked")+`"}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_CLAUDE_FIXTURE", fixture)
			t.Setenv("FAKE_CLAUDE_LIFE_LOG", life)
			var child, replacement int
			t.Cleanup(func() {
				for _, pid := range []int{child, replacement} {
					if pid > 0 {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			// Unlike sleep, this child cannot expire while a slow test is
			// still checking that provider cleanup leaves it alone.
			unrelated := exec.Command("cat")
			stdin, err := unrelated.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, writer, err := os.Pipe()
			if err != nil {
				_ = stdin.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = output.Close(); _ = writer.Close() })
			unrelated.Stdout = writer
			if err := unrelated.Start(); err != nil {
				_ = stdin.Close()
				t.Fatal(err)
			}
			_ = writer.Close()
			t.Cleanup(func() {
				_ = stdin.Close()
				_ = unrelated.Process.Kill()
				_ = unrelated.Wait()
			})
			t.Run("lifetime", func(t *testing.T) {
				bin, pids := filepath.Join(dir, "bin"), filepath.Join(dir, "pids")
				InstallFakeTmux(t, bin, pids)
				binary := InstallBinary(t, filepath.Join(bin, "claude"))
				t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", pids)
				args := []string{"-L", "x", "-f", "/dev/null", "new-session", "-d", "-s", "blocked", "--",
					"/bin/sh", "-c", `"$1" --session-id blocked & printf '%s' "$!" > "$2"; wait`, "sh", binary, childFile}
				if out, err := exec.Command(filepath.Join(bin, "tmux"), args...).CombinedOutput(); err != nil {
					t.Fatalf("launch: %v\n%s", err, out)
				}
				testutil.WaitFor(t, 5*time.Second, "blocked child started", func() bool {
					data, _ := os.ReadFile(childFile)
					child, _ = strconv.Atoi(string(data))
					_, err := os.Stat(life)
					return child > 0 && err == nil
				})
				switch mode {
				case "crashed supervisor", "same-name relaunch":
					data, err := os.ReadFile(filepath.Join(pids, "blocked.pid"))
					if err != nil {
						t.Fatal(err)
					}
					pid, _ := strconv.Atoi(string(data))
					if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
						t.Fatal(err)
					}
					if mode == "same-name relaunch" {
						replacementFile := filepath.Join(dir, "replacement")
						args[len(args)-1] = replacementFile
						if out, err := exec.Command(filepath.Join(bin, "tmux"), args...).CombinedOutput(); err != nil {
							t.Fatalf("relaunch: %v\n%s", err, out)
						}
						data, err := os.ReadFile(filepath.Join(pids, "blocked.pid"))
						if err != nil || string(data) == strconv.Itoa(pid) {
							t.Errorf("relaunch reported success with a stale PID: %s, %v", data, err)
						}
						testutil.WaitFor(t, 5*time.Second, "replacement child started", func() bool {
							data, _ := os.ReadFile(replacementFile)
							replacement, _ = strconv.Atoi(string(data))
							return replacement > 0
						})
					}
				case "kill-session":
					args = []string{"-L", "x", "-f", "/dev/null", "kill-session", "-t", "=blocked"}
					if out, err := exec.Command(filepath.Join(bin, "tmux"), args...).CombinedOutput(); err != nil {
						t.Fatalf("stop: %v\n%s", err, out)
					}
					testutil.WaitFor(t, 2*time.Second, "kill-session stopped child", func() bool { return !processRunning(child) })
				}
			})
			testutil.WaitFor(t, 2*time.Second, "test cleanup stopped child", func() bool { return !processRunning(child) })
			if replacement > 0 {
				testutil.WaitFor(t, 2*time.Second, "test cleanup stopped replacement", func() bool { return !processRunning(replacement) })
			}
			// An echo after cleanup proves it is still responsive, without
			// racing a ps snapshot against process exit or goroutine scheduling.
			if _, err := io.WriteString(stdin, "alive"); err != nil {
				t.Fatalf("cleanup stopped an unrelated process: %v", err)
			}
			if err := output.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var echo [5]byte
			if _, err := io.ReadFull(output, echo[:]); err != nil || string(echo[:]) != "alive" {
				t.Fatalf("unrelated process did not respond: %q, %v", echo, err)
			}
		})
	}
}

func processRunning(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && strings.TrimSpace(string(out)) != "" && !strings.Contains(string(out), "Z")
}
