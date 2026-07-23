package dagu

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type supervisorRunner struct {
	args    []string
	env     []string
	process *supervisorProcess
	starts  int
}

func (r *supervisorRunner) LookPath(string) (string, error) { return "/bin/dagu", nil }
func (r *supervisorRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return []byte("2.1.0"), nil
}
func (r *supervisorRunner) Start(_ string, args, env []string) (Process, error) {
	r.starts++
	r.args = append([]string(nil), args...)
	r.env = append([]string(nil), env...)
	r.process = &supervisorProcess{done: make(chan struct{})}
	return r.process, nil
}

func TestManagerRestartsExitedServer(t *testing.T) {
	runner := &supervisorRunner{}
	manager := NewManager(t.TempDir(), runner, &http.Client{Transport: healthTransport{}})
	manager.waitInterval = 0
	if err := manager.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = runner.process.Kill()
	for i := 0; i < 100; i++ {
		manager.mu.Lock()
		exited := manager.process == nil
		manager.mu.Unlock()
		if exited {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.starts != 2 {
		t.Fatalf("starts = %d", runner.starts)
	}
	_ = manager.Close()
}

type supervisorProcess struct {
	done   chan struct{}
	killed bool
}

func (p *supervisorProcess) Wait() error { <-p.done; return nil }
func (p *supervisorProcess) Kill() error {
	if !p.killed {
		p.killed = true
		close(p.done)
	}
	return nil
}

type healthTransport struct{}

func (healthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != "/api/v1/health" {
		return nil, errors.New("unexpected request")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
}

func TestManagerStartsPrivateServerAndStopsIt(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret")
	runner := &supervisorRunner{}
	manager := NewManager(t.TempDir(), runner, &http.Client{Transport: healthTransport{}})
	manager.waitInterval = 0

	if err := manager.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(runner.args, " ")
	if !strings.Contains(args, "server --host 127.0.0.1 --port") {
		t.Fatalf("args = %q", args)
	}
	if got := envValue(runner.env, "DAGU_HOME"); got != manager.home {
		t.Fatalf("DAGU_HOME = %q, want %q", got, manager.home)
	}
	if got := envValue(runner.env, "HOME"); got != manager.home {
		t.Fatalf("HOME = %q, want private home %q", got, manager.home)
	}
	if got := envValue(runner.env, "DAGU_AUTH_MODE"); got != "none" {
		t.Fatalf("DAGU_AUTH_MODE = %q", got)
	}
	if got := envValue(runner.env, "OPENAI_API_KEY"); got != "" {
		t.Fatalf("Dagu received LLM credential")
	}
	if err := manager.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil || !runner.process.killed {
		t.Fatalf("Close() = %v, killed = %v", err, runner.process.killed)
	}
}

func envValue(env []string, name string) string {
	prefix := name + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}
