package dagu

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
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
	if err := os.MkdirAll(m.home, 0700); err != nil {
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
	process, err := m.runner.Start(path, []string{"server", "--host", "127.0.0.1", "--port", strconv.Itoa(port)}, processEnvironment(m.home))
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
	return NewClient(m.currentEndpoint(), m.http).Start(ctx, definition)
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
