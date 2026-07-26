package dagu

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

type Manager struct {
	home         string
	runner       ManagerRunner
	http         *http.Client
	waitTimeout  time.Duration
	waitInterval time.Duration

	mu       sync.Mutex
	process  Process
	endpoint string
	// resolveVersion resolves a map node's pinned subworkflow version.
	// Nil rejects mapping workflows rather than starting a parent whose
	// child DAGs cannot be produced.
	resolveVersion func(string) (workflows.Definition, error)
	// ocmanEndpoint is handed to the Dagu process so the shim it spawns
	// can call back into ocman.
	ocmanEndpoint string
	// shim is the binary compiled specs invoke for agent, approval,
	// join, and condition steps. Defaults to the running executable so a
	// step always runs the same build as the server that scheduled it.
	shim string
}

// SetOcmanEndpoint records the URL the workflow-step shim calls back on.
func (m *Manager) SetOcmanEndpoint(endpoint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ocmanEndpoint = endpoint
}

// DAGsDir is where compiled child DAGs live. Dagu resolves `dag.run`
// targets by name from this directory, so a mapped child must be on
// disk before its parent starts.
func (m *Manager) DAGsDir() string { return filepath.Join(m.home, "dags") }

// SetVersionResolver supplies the lookup used to compile a map node's
// pinned per-item subworkflow.
func (m *Manager) SetVersionResolver(resolve func(string) (workflows.Definition, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolveVersion = resolve
}

func NewManager(home string, runner ManagerRunner, client *http.Client) *Manager {
	if runner == nil {
		runner = osRunner{}
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Manager{home: home, runner: runner, http: client, waitTimeout: 15 * time.Second, waitInterval: 200 * time.Millisecond}
}

func (m *Manager) Status(ctx context.Context) Result {
	return NewDetector(m.runner, runtime.GOOS).Status(ctx)
}

func (m *Manager) Ensure(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process != nil && m.healthy(ctx) {
		return nil
	}
	if m.process != nil {
		_ = m.process.Kill()
		m.process = nil
		m.endpoint = ""
	}
	status := NewDetector(m.runner, runtime.GOOS).Status(ctx)
	if status.Status != Compatible {
		return fmt.Errorf("dagu 2.x is not available")
	}
	if err := os.MkdirAll(filepath.Join(m.home, "dags"), 0700); err != nil {
		return fmt.Errorf("create Dagu home: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("allocate Dagu port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	path, err := m.runner.LookPath("dagu")
	if err != nil {
		return err
	}
	// `server` and not `start-all`: ocman owns every schedule, so dagu
	// runs no scheduler and only executes runs ocman posts.
	process, err := m.runner.Start(path, []string{"server", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--dags", filepath.Join(m.home, "dags")}, processEnvironment(m.home, m.ocmanEndpoint))
	if err != nil {
		return fmt.Errorf("start Dagu: %w", err)
	}
	m.process = process
	m.endpoint = "http://127.0.0.1:" + strconv.Itoa(port)
	go func(current Process) {
		_ = current.Wait()
		m.mu.Lock()
		if m.process == current {
			m.process = nil
			m.endpoint = ""
		}
		m.mu.Unlock()
	}(process)

	deadline := time.Now().Add(m.waitTimeout)
	for {
		if m.healthy(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			_ = process.Kill()
			m.process = nil
			m.endpoint = ""
			return fmt.Errorf("dagu did not become healthy")
		}
		if m.waitInterval > 0 {
			select {
			case <-ctx.Done():
				_ = process.Kill()
				m.process = nil
				m.endpoint = ""
				return ctx.Err()
			case <-time.After(m.waitInterval):
			}
		}
	}
}

func (m *Manager) healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint+"/api/v1/health", nil)
	if err != nil {
		return false
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *Manager) Start(ctx context.Context, definition workflows.Definition) (Run, error) {
	if err := m.Ensure(ctx); err != nil {
		return Run{}, err
	}
	id, err := newRunID()
	if err != nil {
		return Run{}, err
	}
	m.mu.Lock()
	resolve := m.resolveVersion
	m.mu.Unlock()
	compiled, err := Compile(definition, CompileOptions{RunID: id, ResolveVersion: resolve, Shim: m.stepShim()})
	if err != nil {
		return Run{}, err
	}
	return m.StartCompiled(ctx, definition.ID, id, compiled)
}

// CompileRun renders a definition for this manager, using its version
// resolver and shim. It is separate from starting so a caller can decide
// whether the runner supports a definition before creating anything.
func (m *Manager) CompileRun(definition workflows.Definition, runID string) (Compiled, error) {
	m.mu.Lock()
	resolve := m.resolveVersion
	m.mu.Unlock()
	return Compile(definition, CompileOptions{RunID: runID, ResolveVersion: resolve, Shim: m.stepShim()})
}

// StartCompiled posts an already-compiled run under a caller-chosen id,
// which lets ocman use its own run id as the Dagu run id.
func (m *Manager) StartCompiled(ctx context.Context, name, id string, compiled Compiled) (Run, error) {
	if err := m.Ensure(ctx); err != nil {
		return Run{}, err
	}
	// Children must land before the parent starts, or dagu resolves
	// `dag.run` against a name that does not exist yet.
	for child, spec := range compiled.Children {
		target := filepath.Join(m.DAGsDir(), child+".yaml")
		if err := os.WriteFile(target, spec, 0600); err != nil {
			return Run{}, fmt.Errorf("write child DAG %s: %w", child, err)
		}
	}
	return NewClient(m.currentEndpoint(), m.http).StartSpec(ctx, name, id, compiled.Spec)
}

func (m *Manager) GetRun(ctx context.Context, name, id string) (Run, error) {
	if err := m.Ensure(ctx); err != nil {
		return Run{}, err
	}
	return NewClient(m.currentEndpoint(), m.http).GetRun(ctx, name, id)
}

func (m *Manager) Cancel(ctx context.Context, name, id string) error {
	if err := m.Ensure(ctx); err != nil {
		return err
	}
	return NewClient(m.currentEndpoint(), m.http).Cancel(ctx, name, id)
}

// stepShim resolves the binary a compiled spec invokes for shim steps.
// Falling back to PATH keeps a test or an unusual deployment working.
func (m *Manager) stepShim() string {
	m.mu.Lock()
	configured := m.shim
	m.mu.Unlock()
	if configured != "" {
		return configured
	}
	if executable, err := os.Executable(); err == nil {
		return executable
	}
	return ""
}

func (m *Manager) currentEndpoint() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.endpoint
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process == nil {
		return nil
	}
	err := m.process.Kill()
	m.process = nil
	m.endpoint = ""
	return err
}
