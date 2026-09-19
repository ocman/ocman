package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestServeHelper is a real child process; its script only selects a test mode.
func TestServeHelper(t *testing.T) {
	if len(os.Args) != 6 || os.Args[2] != "--" {
		return
	}
	mode := os.Args[3]
	cwd, _ := os.Getwd()
	if os.Args[4] != "serve" || cwd != os.Args[5] || os.Getenv("HOME") != "" || os.Getenv("OCMAN_AUTH_PASSWORD") != "" || os.Getenv("GITHUB_TOKEN") != "" {
		os.Exit(2)
	}
	f, _ := os.OpenFile("starts", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = fmt.Fprintln(f, os.Getpid())
	_ = f.Close()
	e := hello(ModeServe)
	e.Hello.Token = os.Getenv("OCMAN_PLUGIN_TOKEN")
	switch mode {
	case "no-hello":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "bad-token":
		e.Hello.Token = testToken
	case "changed-offer":
		e.Hello.Description.Name = "Changed"
	case "crash":
		os.Exit(1)
	case "partial":
		fmt.Print(`{"type":`)
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(e)
	dec := NewDecoder(os.Stdin)
	ack, err := dec.Decode()
	if err != nil || ack.Hello == nil || ack.Hello.Accepted == nil {
		os.Exit(3)
	}
	if mode == "stderr" {
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("private-secret\n", MaxStderrBytes))
	}
	if mode == "no-read" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if mode == "descendant" {
		child := exec.Command("/bin/sh", "-c", "sleep 30")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(4)
		}
		_ = os.WriteFile("descendant", []byte(strconv.Itoa(child.Process.Pid)), 0600)
	}
	var waiting []Call
	for {
		e, err := dec.Decode()
		if err != nil {
			os.Exit(0)
		}
		switch e.Type {
		case TypeCall:
			c := *e.Call
			f, _ := os.OpenFile("calls", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = fmt.Fprintln(f, c.OperationID)
			_ = f.Close()
			switch c.Method {
			case "hold":
				_ = enc.Encode(chunk(c.ID, 1))
				continue
			case "crash":
				os.Exit(1)
			case "malformed":
				fmt.Println("sensitive malformed stdout")
				continue
			case "overflow":
				fmt.Print(strings.Repeat("x", MaxMessageBytes+3))
				continue
			case "unknown-id":
				_ = enc.Encode(result(c.ID + 1))
				continue
			case "chunk-order":
				_ = enc.Encode(chunk(c.ID, 2))
				continue
			case "flood":
				for i := uint64(1); i <= 100; i++ {
					_ = enc.Encode(chunk(c.ID, i))
				}
				continue
			case "large-output":
				for i := uint64(1); i <= 20; i++ {
					part := chunk(c.ID, i)
					part.Chunk.Data = json.RawMessage(`"` + strings.Repeat("x", MaxMessageBytes/2) + `"`)
					_ = enc.Encode(part)
				}
				continue
			case "events":
				for i := 0; i < 100; i++ {
					_ = enc.Encode(Envelope{Type: TypeEvent, Event: &Event{Capability: "action", Name: "changed", Data: json.RawMessage(`null`)}})
				}
				continue
			case "pair":
				waiting = append(waiting, c)
				if len(waiting) != 2 {
					continue
				}
				for i := len(waiting) - 1; i >= 0; i-- {
					_ = enc.Encode(chunk(waiting[i].ID, 1))
					r := result(waiting[i].ID)
					r.Result.Value, _ = json.Marshal(waiting[i].OperationID)
					_ = enc.Encode(r)
				}
				waiting = nil
				continue
			}
			_ = enc.Encode(chunk(c.ID, 1))
			r := result(c.ID)
			r.Result.Value, _ = json.Marshal(c.OperationID)
			_ = enc.Encode(r)
		case TypeCancel:
			_ = os.WriteFile("cancelled", []byte(strconv.FormatUint(e.Cancel.ID, 10)), 0600)
			if mode != "ignore-cancel" {
				_ = enc.Encode(result(e.Cancel.ID))
			}
		case TypeShutdown:
			_ = os.WriteFile("shutdown", []byte("yes"), 0600)
			if mode == "ignore-shutdown" {
				time.Sleep(10 * time.Second)
			}
			os.Exit(0)
		}
	}
}

func processFixture(t *testing.T, mode string) LaunchConfig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	warmDescribeHelper.Do(func() { warmDescribeHelperErr = exec.Command(exe, "-test.run=^$").Run() })
	if warmDescribeHelperErr != nil {
		t.Fatal(warmDescribeHelperErr)
	}
	dir := t.TempDir()
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ocman-plugin-test")
	script := fmt.Sprintf("#!/bin/sh\nexport GORACE=atexit_sleep_ms=0\nexec %q -test.run=^TestServeHelper$ -- %q \"$@\" %q\n", exe, mode, dir)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	checksum, err := executableChecksum(path, info)
	if err != nil {
		t.Fatal(err)
	}
	return LaunchConfig{Candidate: Discovery{Path: path, Checksum: checksum, Description: *hello(ModeServe).Hello.Description}, DataDir: dir, Supported: []Capability{{Name: "action", Version: Version{1, 0}}}}
}

func testProcess(t *testing.T, mode string, restarts int) (*Process, LaunchConfig) {
	t.Helper()
	config := processFixture(t, mode)
	p, err := startProcess(context.Background(), config, processPolicy{3 * time.Second, 500 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, restarts})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	return p, config
}

func await(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func processCall(t *testing.T, p *Process, ctx context.Context, method, operation string, deadline time.Duration) <-chan Reply {
	t.Helper()
	c := *call(1).Call
	c.Method, c.OperationID, c.DeadlineUnixMS = method, operation, time.Now().Add(deadline).UnixMilli()
	replies, err := p.Call(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return replies
}

func nextReply(t *testing.T, replies <-chan Reply) Reply {
	t.Helper()
	select {
	case r, ok := <-replies:
		if !ok {
			t.Fatal("unexpected closed replies")
		}
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("reply timed out")
		return Reply{}
	}
}

func TestProcessReadinessAndMultiplexing(t *testing.T) {
	t.Setenv("OCMAN_AUTH_PASSWORD", "host-secret")
	t.Setenv("GITHUB_TOKEN", "forge-secret")
	p, config := testProcess(t, "success", 0)
	await(t, func() bool { return p.Health().Status == "ready" })
	a := processCall(t, p, context.Background(), "pair", "first", time.Second)
	b := processCall(t, p, context.Background(), "pair", "second", time.Second)
	for i, ch := range []<-chan Reply{a, b} {
		part, result := nextReply(t, ch), nextReply(t, ch)
		want := []string{`"first"`, `"second"`}[i]
		if part.Message.Chunk == nil || result.Message.Result == nil || string(result.Message.Result.Value) != want || part.Message.Chunk.ID != uint64(i+1) {
			t.Fatalf("bad multiplexed replies: %+v %+v", part, result)
		}
		if _, ok := <-ch; ok {
			t.Fatal("result not terminal")
		}
	}
	p.Stop()
	data, err := os.ReadFile(filepath.Join(config.DataDir, "shutdown"))
	if err != nil || string(data) != "yes" || p.Health().Status != "stopped" {
		t.Fatalf("shutdown: %s %v %+v", data, err, p.Health())
	}
}

func TestProcessReadinessFailuresAndRestartCutoff(t *testing.T) {
	for _, mode := range []string{"no-hello", "partial", "bad-token", "changed-offer", "crash"} {
		t.Run(mode, func(t *testing.T) {
			p, config := testProcess(t, mode, 1)
			await(t, func() bool { return p.Health().Status == "unhealthy" })
			h := p.Health()
			if h.RestartCount != 1 || h.LastError == "" {
				t.Fatalf("health: %+v", h)
			}
			data, err := os.ReadFile(filepath.Join(config.DataDir, "starts"))
			if err != nil || len(strings.Fields(string(data))) != 2 {
				t.Fatalf("starts: %q %v", data, err)
			}
			<-p.done
			if _, err := p.Call(context.Background(), *call(1).Call); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("terminal call: %v", err)
			}
		})
	}
}

func TestProcessDeadlineCancellationAndConcurrency(t *testing.T) {
	for _, mode := range []string{"success", "ignore-cancel"} {
		t.Run(mode, func(t *testing.T) {
			p, config := testProcess(t, mode, 0)
			await(t, func() bool { return p.Health().Status == "ready" })
			ctx, cancelCall := context.WithCancel(context.Background())
			defer cancelCall()
			a := processCall(t, p, ctx, "hold", "cancel-me", time.Second)
			b := processCall(t, p, context.Background(), "hold", "timeout", 70*time.Millisecond)
			_ = nextReply(t, a)
			_ = nextReply(t, b)
			busy := processCall(t, p, context.Background(), "invoke", "busy", time.Second)
			if r := nextReply(t, busy); !errors.Is(r.Err, ErrBusy) {
				t.Fatalf("busy: %+v", r)
			}
			cancelCall()
			if r := nextReply(t, a); !errors.Is(r.Err, context.Canceled) {
				t.Fatalf("cancel: %+v", r)
			}
			if r := nextReply(t, b); !errors.Is(r.Err, context.DeadlineExceeded) {
				t.Fatalf("deadline: %+v", r)
			}
			await(t, func() bool { _, err := os.Stat(filepath.Join(config.DataDir, "cancelled")); return err == nil })
			if mode == "ignore-cancel" {
				await(t, func() bool { return p.Health().Status == "unhealthy" })
				return
			}
			// Results acknowledging cancellation eventually release both slots.
			await(t, func() bool {
				ch := processCall(t, p, context.Background(), "invoke", "after-cancel", time.Second)
				r := nextReply(t, ch)
				if errors.Is(r.Err, ErrBusy) {
					return false
				}
				if r.Message.Chunk == nil || nextReply(t, ch).Message.Result == nil {
					t.Fatal("call after cancellation failed")
				}
				return true
			})
		})
	}
}

func TestProcessMalformedOutputAndLimits(t *testing.T) {
	for _, method := range []string{"malformed", "overflow", "unknown-id", "chunk-order", "flood", "large-output", "events"} {
		t.Run(method, func(t *testing.T) {
			p, _ := testProcess(t, "success", 0)
			await(t, func() bool { return p.Health().Status == "ready" })
			replies := processCall(t, p, context.Background(), method, "bad-output", 4*time.Second)
			if method == "flood" {
				await(t, func() bool { return p.Health().Status == "unhealthy" })
			}
			for {
				r := nextReply(t, replies)
				if r.Err != nil {
					break
				}
			}
			await(t, func() bool { return p.Health().Status == "unhealthy" })
			if strings.Contains(p.Health().LastError, "sensitive") {
				t.Fatal("stdout leaked")
			}
		})
	}
}

func TestProcessCrashIsolationAndNoReplay(t *testing.T) {
	p, config := testProcess(t, "success", 1)
	other, _ := testProcess(t, "success", 0)
	await(t, func() bool { return p.Health().Status == "ready" && other.Health().Status == "ready" })
	crashed := processCall(t, p, context.Background(), "crash", "never-replay", time.Second)
	if r := nextReply(t, crashed); !errors.Is(r.Err, ErrUnavailable) {
		t.Fatalf("crash: %+v", r)
	}
	await(t, func() bool { h := p.Health(); return h.Status == "ready" && h.RestartCount == 1 })
	replies := processCall(t, other, context.Background(), "invoke", "unaffected", time.Second)
	_ = nextReply(t, replies)
	if nextReply(t, replies).Message.Result == nil {
		t.Fatal("other process failed")
	}
	p.Stop()
	data, _ := os.ReadFile(filepath.Join(config.DataDir, "calls"))
	if string(data) != "never-replay\n" {
		t.Fatalf("replayed: %q", data)
	}
}

func TestProcessStderrBoundsAndRedaction(t *testing.T) {
	p, _ := testProcess(t, "stderr", 0)
	await(t, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.stderr) == MaxStderrBytes })
	if h := p.Health(); strings.Contains(fmt.Sprint(h), "private-secret") {
		t.Fatalf("stderr leaked: %+v", h)
	}
	replies := processCall(t, p, context.Background(), "crash", "crash", time.Second)
	if r := nextReply(t, replies); r.Err == nil || strings.Contains(r.Err.Error(), "private-secret") {
		t.Fatalf("stderr leaked: %+v", r)
	}
	await(t, func() bool { return p.Health().Status == "unhealthy" })
	if strings.Contains(p.Health().LastError, "private-secret") {
		t.Fatal("health leaked stderr")
	}
}

func TestProcessShutdownKillsAndSettles(t *testing.T) {
	for _, mode := range []string{"ignore-shutdown", "descendant", "no-read"} {
		t.Run(mode, func(t *testing.T) {
			p, config := testProcess(t, mode, 2)
			await(t, func() bool { return p.Health().Status == "ready" })
			replies := processCall(t, p, context.Background(), "hold", "abandoned", time.Second)
			if mode != "no-read" {
				_ = nextReply(t, replies)
			}
			start := time.Now()
			p.Stop()
			p.Stop()
			if time.Since(start) > time.Second {
				t.Fatal("unbounded shutdown")
			}
			if r := nextReply(t, replies); !errors.Is(r.Err, ErrUnavailable) {
				t.Fatalf("unsettled: %+v", r)
			}
			data, _ := os.ReadFile(filepath.Join(config.DataDir, "starts"))
			pids := strings.Fields(string(data))
			if len(pids) != 1 {
				t.Fatalf("restarted during shutdown: %q", data)
			}
			pid, _ := strconv.Atoi(pids[0])
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("child still alive: %v", err)
			}
			if mode == "descendant" {
				data, _ := os.ReadFile(filepath.Join(config.DataDir, "descendant"))
				pid, _ := strconv.Atoi(string(data))
				await(t, func() bool {
					if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
						return true
					}
					// Minimal Linux containers may not reap orphan zombies promptly.
					if runtime.GOOS == "linux" {
						stat, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
						return strings.Contains(string(stat), ") Z ")
					}
					return false
				})
			}
		})
	}
}

func TestProcessBlockedWriteDeadline(t *testing.T) {
	p, _ := testProcess(t, "no-read", 0)
	await(t, func() bool { return p.Health().Status == "ready" })
	c := *call(1).Call
	c.Params = json.RawMessage(`"` + strings.Repeat("x", MaxMessageBytes/2) + `"`)
	c.DeadlineUnixMS = time.Now().Add(100 * time.Millisecond).UnixMilli()
	ch, err := p.Call(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if r := nextReply(t, ch); !errors.Is(r.Err, context.DeadlineExceeded) {
		t.Fatalf("blocked write: %+v", r)
	}
	await(t, func() bool { return p.Health().Status == "unhealthy" })
}

func TestProcessBoundedBackoff(t *testing.T) {
	config := processFixture(t, "crash")
	var starts []time.Time
	config.OnHealth = func(h Health) {
		if h.Status == "starting" && h.LastError == "" {
			starts = append(starts, time.Now())
		}
	}
	p, err := startProcess(context.Background(), config, processPolicy{time.Second, 100 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond, 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("restart loop did not terminate")
	}
	if len(starts) != 4 {
		t.Fatalf("launch count: %d", len(starts))
	}
	for i, minimum := range []time.Duration{100 * time.Millisecond, 150 * time.Millisecond, 150 * time.Millisecond} {
		if elapsed := starts[i+1].Sub(starts[i]); elapsed < minimum || elapsed > time.Second {
			t.Fatalf("backoff %d: %s", i, elapsed)
		}
	}
	if p.Health().Status != "unhealthy" {
		t.Fatal("missing cutoff")
	}
}

func TestProcessRejectsUnapprovedAndInvalidCalls(t *testing.T) {
	config := processFixture(t, "success")
	config.Candidate.Checksum = strings.Repeat("0", 64)
	p, err := startProcess(context.Background(), config, processPolicy{time.Second, time.Millisecond, time.Millisecond, time.Millisecond, 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	await(t, func() bool { return p.Health().Status == "unhealthy" })
	if _, err := os.Stat(filepath.Join(config.DataDir, "starts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unapproved executable launched")
	}
	config.Candidate.Err = ErrDuplicateID
	if _, err := StartProcess(context.Background(), config); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("conflict: %v", err)
	}
	good, _ := testProcess(t, "success", 0)
	await(t, func() bool { return good.Health().Status == "ready" })
	for _, kind := range []string{"invalid", "expired", "cancelled", "oversized", "unsupported", "deep"} {
		c := *call(1).Call
		ctx, cancelCall := context.WithCancel(context.Background())
		switch kind {
		case "invalid":
			c.OperationID = ""
		case "expired":
			c.DeadlineUnixMS = 1
		case "cancelled":
			cancelCall()
		case "oversized":
			c.Params = json.RawMessage(`"` + strings.Repeat("x", MaxMessageBytes) + `"`)
		case "unsupported":
			c.Capability = "unknown"
		case "deep":
			c.Params = json.RawMessage(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65))
		}
		ch, err := good.Call(ctx, c)
		if err == nil {
			err = nextReply(t, ch).Err
		}
		cancelCall()
		if err == nil {
			t.Fatalf("accepted %s", kind)
		}
		if good.Health().Status != "ready" {
			t.Fatalf("bad caller killed process: %s", kind)
		}
	}
}
