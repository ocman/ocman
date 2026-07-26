package dagu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

// daguAPI fakes the subset of Dagu's HTTP API the manager drives, so the
// start/read/cancel paths are exercised without a Dagu binary.
type daguAPI struct {
	health   int
	runs     []string
	stops    []string
	detail   string
	logs     int
	specSeen string
}

func (a *daguAPI) client() *http.Client { return &http.Client{Transport: a} }

func (a *daguAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	respond := func(status int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}
	path := req.URL.Path
	switch {
	case path == "/api/v1/health":
		status := a.health
		if status == 0 {
			status = http.StatusOK
		}
		return respond(status, "")
	case path == "/api/v1/dag-runs" && req.Method == http.MethodPost:
		body, _ := io.ReadAll(req.Body)
		a.specSeen = string(body)
		var decoded struct {
			DAGRunID string `json:"dagRunId"`
		}
		// Echo the requested run id back, which is what lets ocman use
		// its own run id as the Dagu run id.
		if start := strings.Index(a.specSeen, `"dagRunId":"`); start >= 0 {
			rest := a.specSeen[start+len(`"dagRunId":"`):]
			decoded.DAGRunID = rest[:strings.Index(rest, `"`)]
		}
		a.runs = append(a.runs, decoded.DAGRunID)
		return respond(http.StatusOK, fmt.Sprintf(`{"dagRunId":%q}`, decoded.DAGRunID))
	case strings.HasSuffix(path, "/stop"):
		a.stops = append(a.stops, path)
		return respond(http.StatusOK, "")
	case strings.Contains(path, "/steps/"):
		a.logs++
		return respond(http.StatusOK, `{"content":"step output\n"}`)
	case strings.HasPrefix(path, "/api/v1/dag-runs/"):
		if a.detail == "" {
			return respond(http.StatusNotFound, "no such run")
		}
		return respond(http.StatusOK, a.detail)
	}
	return respond(http.StatusNotFound, "unhandled "+path)
}

func startedManager(t *testing.T, api *daguAPI) (*Manager, *supervisorRunner) {
	t.Helper()
	runner := &supervisorRunner{}
	manager := NewManager(t.TempDir(), runner, api.client())
	manager.waitInterval = 0
	t.Cleanup(func() { _ = manager.Close() })
	return manager, runner
}

// A mapped run's child DAGs must be on disk before the parent starts, or
// Dagu resolves `dag.run` against a name that does not exist yet.
func TestManagerStartCompiledWritesChildrenBeforePosting(t *testing.T) {
	api := &daguAPI{}
	manager, _ := startedManager(t, api)
	compiled := Compiled{
		Spec:     []byte("steps: []\n"),
		Children: map[string][]byte{"child-item": []byte("steps: []\n")},
	}
	run, err := manager.StartCompiled(context.Background(), "release", "run-1", compiled)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "run-1" || run.Name != "release" {
		t.Fatalf("run = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(manager.DAGsDir(), "child-item.yaml")); err != nil {
		t.Fatalf("child DAG not written: %v", err)
	}
	if len(api.runs) != 1 || api.runs[0] != "run-1" {
		t.Fatalf("posted runs = %v", api.runs)
	}
}

// An unwritable DAGs directory has to fail the start rather than post a
// parent whose children are missing.
func TestManagerStartCompiledFailsOnUnwritableChild(t *testing.T) {
	api := &daguAPI{}
	manager, _ := startedManager(t, api)
	compiled := Compiled{Spec: []byte("steps: []\n"), Children: map[string][]byte{"nested/child": nil}}
	if _, err := manager.StartCompiled(context.Background(), "release", "run-1", compiled); err == nil {
		t.Fatal("want error for an unwritable child DAG")
	}
	if len(api.runs) != 0 {
		t.Fatalf("posted despite child failure: %v", api.runs)
	}
}

func TestManagerGetRunProjectsStepsAndLogs(t *testing.T) {
	api := &daguAPI{detail: `{"dagRunDetails":{"dagRunId":"run-1","name":"release","statusLabel":"failed",
		"startedAt":"2026-01-01T00:00:00Z","finishedAt":"2026-01-01T00:01:00Z",
		"nodes":[{"step":{"name":"build","depends":["review"]},"statusLabel":"succeeded",
		"startedAt":"2026-01-01T00:00:10Z","finishedAt":"2026-01-01T00:00:20Z"},
		{"step":{"name":"ship"},"statusLabel":"failed","error":"boom"}]}}`}
	manager, _ := startedManager(t, api)
	run, err := manager.GetRun(context.Background(), "release", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" || run.StartedAt == 0 || run.FinishedAt == 0 {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Nodes) != 2 {
		t.Fatalf("nodes = %+v", run.Nodes)
	}
	if run.Nodes[0].Name != "build" || run.Nodes[0].Log != "step output\n" {
		t.Errorf("build = %+v", run.Nodes[0])
	}
	// A step that never ran has no timestamps to project.
	if run.Nodes[1].Error != "boom" || run.Nodes[1].StartedAt != 0 {
		t.Errorf("ship = %+v", run.Nodes[1])
	}
	if api.logs != 2 {
		t.Errorf("log reads = %d, want one per step", api.logs)
	}
}

// A runner outage must surface as an error so the mirror retries instead
// of settling the run.
func TestManagerGetRunSurfacesAPIErrors(t *testing.T) {
	manager, _ := startedManager(t, &daguAPI{})
	if _, err := manager.GetRun(context.Background(), "release", "missing"); err == nil {
		t.Fatal("want error")
	}
}

func TestManagerCancelStopsTheRun(t *testing.T) {
	api := &daguAPI{}
	manager, _ := startedManager(t, api)
	if err := manager.Cancel(context.Background(), "release", "run-1"); err != nil {
		t.Fatal(err)
	}
	if len(api.stops) != 1 || !strings.Contains(api.stops[0], "run-1") {
		t.Fatalf("stops = %v", api.stops)
	}
}

// Dagu 1.x cannot execute a compiled spec, so Ensure must refuse rather
// than start a server that will mis-run the graph.
func TestManagerEnsureRequiresCompatibleDagu(t *testing.T) {
	manager := NewManager(t.TempDir(), &fixedRunner{version: "1.15.0"}, (&daguAPI{}).client())
	manager.waitInterval = 0
	if err := manager.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "dagu 2.x") {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerEnsurePropagatesStartFailure(t *testing.T) {
	manager := NewManager(t.TempDir(), &fixedRunner{version: "2.1.0", start: errors.New("exec format error")}, (&daguAPI{}).client())
	manager.waitInterval = 0
	if err := manager.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "start Dagu") {
		t.Fatalf("err = %v", err)
	}
}

// A server that starts but never answers its health probe must be killed
// rather than left behind as an orphan ocman thinks is running.
func TestManagerEnsureKillsAnUnhealthyServer(t *testing.T) {
	runner := &supervisorRunner{}
	manager := NewManager(t.TempDir(), runner, (&daguAPI{health: http.StatusInternalServerError}).client())
	manager.waitInterval = 0
	manager.waitTimeout = 0
	if err := manager.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "healthy") {
		t.Fatalf("err = %v", err)
	}
	if !runner.process.killed {
		t.Error("unhealthy server was not killed")
	}
	if manager.currentEndpoint() != "" {
		t.Errorf("endpoint = %q, want cleared", manager.currentEndpoint())
	}
}

func TestManagerEnsureAbandonsOnCanceledContext(t *testing.T) {
	runner := &supervisorRunner{}
	manager := NewManager(t.TempDir(), runner, (&daguAPI{health: http.StatusInternalServerError}).client())
	manager.waitInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Ensure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !runner.process.killed {
		t.Error("abandoned server was not killed")
	}
}

func TestManagerCloseWithoutProcessIsANoop(t *testing.T) {
	manager := NewManager(t.TempDir(), &supervisorRunner{}, nil)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

// The shim callback endpoint and version resolver are set after
// construction, and CompileRun has to pick both up.
func TestManagerCompileRunUsesResolverAndShim(t *testing.T) {
	manager := NewManager(t.TempDir(), &supervisorRunner{}, nil)
	manager.SetOcmanEndpoint("http://127.0.0.1:9411")
	if got := manager.OcmanEndpoint(); got != "http://127.0.0.1:9411" {
		t.Fatalf("endpoint = %q", got)
	}
	manager.shim = "/usr/local/bin/ocman"

	resolved := 0
	manager.SetVersionResolver(func(string) (workflows.Definition, error) {
		resolved++
		return workflows.Definition{
			ID: "child", Name: "Child", Directory: "/repo",
			Triggers: []workflows.Trigger{{ID: "manual", Type: workflows.TriggerManual}},
			Nodes: []workflows.Node{{ID: "step", Name: "Step", Type: "command",
				Command: []string{"true"}}},
		}, nil
	})
	definition := workflows.Definition{
		ID: "mapper", Name: "Mapper", Directory: "/repo", Concurrency: 1,
		Triggers: []workflows.Trigger{{ID: "manual", Type: workflows.TriggerManual}},
		Nodes: []workflows.Node{
			{ID: "list", Name: "List", Type: "command", Command: []string{"echo", "[]"}},
			{ID: "each", Name: "Each", Type: "map", Map: &workflows.MapConfig{
				Source: "${nodes.list.output}", Key: "id", Join: "done",
				Subworkflow: workflows.SubworkflowRef{WorkflowID: "child"}, VersionID: "ver-1"}},
			{ID: "done", Name: "Done", Type: "join", Join: &workflows.JoinConfig{Policy: workflows.JoinAllSuccess}},
		},
		Dependencies: []workflows.Dependency{{From: "list", To: "each"}, {From: "each", To: "done"}},
	}
	compiled, err := manager.CompileRun(definition, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved == 0 {
		t.Error("map node did not consult the version resolver")
	}
	if len(compiled.Children) == 0 {
		t.Error("mapped definition produced no child DAGs")
	}
	if !strings.Contains(string(compiled.Spec), "/usr/local/bin/ocman") {
		t.Errorf("spec does not use the configured shim:\n%s", compiled.Spec)
	}
}

// Without a configured shim a step still needs a binary to invoke, and
// the running executable is the only build guaranteed to match.
func TestManagerStepShimFallsBackToTheRunningBinary(t *testing.T) {
	manager := NewManager(t.TempDir(), &supervisorRunner{}, nil)
	executable, err := os.Executable()
	if err != nil {
		t.Skip("no executable path on this platform")
	}
	if got := manager.stepShim(); got != executable {
		t.Fatalf("stepShim() = %q, want %q", got, executable)
	}
}

func TestManagerStatusReportsDetection(t *testing.T) {
	manager := NewManager(t.TempDir(), &fixedRunner{version: "2.1.0"}, nil)
	if got := manager.Status(context.Background()); got.Status != Compatible || got.Version != "2.1.0" {
		t.Fatalf("status = %+v", got)
	}
}

// fixedRunner reports a chosen dagu version and optionally fails to start
// the process.
type fixedRunner struct {
	version string
	start   error
}

func (fixedRunner) LookPath(string) (string, error) { return "/bin/dagu", nil }
func (r fixedRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return []byte(r.version), nil
}
func (r *fixedRunner) Start(string, []string, []string) (Process, error) {
	if r.start != nil {
		return nil, r.start
	}
	return &supervisorProcess{done: make(chan struct{})}, nil
}
