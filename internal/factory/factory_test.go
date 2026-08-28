package factory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeAckStore struct {
	runner *fakeRunner
	calls  [][]any
	err    error
	has    bool
	hasErr error
	checks [][]string
}

type fakeProjectResolver struct {
	root string
	err  error
}

func (r fakeProjectResolver) ResolveLocalProject(context.Context, string) (string, error) {
	return r.root, r.err
}

func (s *fakeAckStore) HasFactoryLocalExecutionAck(_ context.Context, host, repo, profile, version string) (bool, error) {
	s.checks = append(s.checks, []string{host, repo, profile, version})
	return s.has, s.hasErr
}

func (s *fakeAckStore) UpsertFactoryLocalExecutionAck(_ context.Context, host, repo, profile, version, actor string, at time.Time) error {
	if s.runner != nil && len(s.calls) == 0 && len(s.runner.seen) != 0 {
		return errors.New("acknowledgement happened after Beads access")
	}
	s.calls = append(s.calls, []any{host, repo, profile, version, actor, at})
	return s.err
}

type fakeRunner struct {
	pathErr error
	runs    []fakeRun
	seen    [][]string
	envs    [][]string
	plans   [][]byte
	onRun   func(context.Context, []string)
}

type fakeRun struct {
	out string
	err error
}

func (f *fakeRunner) LookPath(string) (string, error) { return "/usr/bin/bd", f.pathErr }

func (f *fakeRunner) Run(ctx context.Context, _ string, _ string, args, env []string) ([]byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	f.seen = append(f.seen, append([]string(nil), args...))
	f.envs = append(f.envs, append([]string(nil), env...))
	if f.onRun != nil {
		f.onRun(ctx, args)
	}
	if len(args) >= 3 && args[0] == "create" && args[1] == "--graph" {
		plan, _ := os.ReadFile(args[2])
		f.plans = append(f.plans, plan)
	}
	if len(f.seen) > len(f.runs) {
		return nil, nil, nil
	}
	run := f.runs[len(f.seen)-1]
	return []byte(run.out), nil, run.err
}

const versionEnvelope = `{"schema_version":1,"data":{"version":"1.1.0"}}`

func versionResult(version string) string {
	return `{"schema_version":1,"data":{"version":` + strconv.Quote(version) + `,"future":"ignored"}}`
}

func listEnvelope(issues string) string {
	return `{"schema_version":1,"data":` + issues + `}`
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

const pouredIssues = `[
  {"id":"fac-1","title":"Ship search","status":"open","issue_type":"epic","metadata":{"ocman.contract":"1","ocman.kind":"work-epic","ocman.formula_id":"ocman/default","ocman.formula_version":"1","ocman.formula_origin":"built-in","ocman.instantiation_id":"intake-42","ocman.goal":"Ship search","ocman.initial_project":"/repo","ocman.planning_work_id":"fac-1.1","ocman.plan_approval_gate_id":"fac-1.2"}},
  {"id":"fac-1.1","title":"Plan: Ship search","status":"in_progress","issue_type":"task","metadata":{"ocman.contract":"1","ocman.kind":"agent-work","ocman.formula_id":"ocman/default","ocman.formula_version":"1","ocman.formula_origin":"built-in","ocman.instantiation_id":"intake-42","ocman.work_epic_id":"fac-1","ocman.permission_profile":"factory-plan/v1"}},
  {"id":"fac-1.2","title":"Plan approval","status":"open","issue_type":"gate","metadata":{"ocman.contract":"1","ocman.kind":"gate","ocman.formula_id":"ocman/default","ocman.formula_version":"1","ocman.formula_origin":"built-in","ocman.instantiation_id":"intake-42","ocman.work_epic_id":"fac-1","ocman.gate_type":"plan-approval"}}
]`

func pouredIssuesAt(project string) string {
	return strings.ReplaceAll(pouredIssues, `"/repo"`, strconv.Quote(project))
}

func pouredPreparedIssues(project, key, brief string) string {
	issues := strings.ReplaceAll(currentPouredIssuesAt(project), "intake-42", key)
	issues = strings.Replace(issues, `"title":"Ship search",`, `"title":"Ship search","description":`+strconv.Quote(brief)+`,`, 1)
	return strings.Replace(issues, `"title":"Plan: Ship search",`, `"title":"Plan: Ship search","description":`+strconv.Quote(brief)+`,`, 1)
}

func currentPouredIssuesAt(project string) string {
	issues := strings.ReplaceAll(pouredIssuesAt(project), `"ocman.formula_version":"1"`, `"ocman.formula_revision":"2","ocman.formula_version":"2","ocman.formula_hash":"`+formulaHash(defaultFormulaYAML)+`"`)
	return issues
}

func TestProbeBeadsCompatibility(t *testing.T) {
	tests := []struct {
		name   string
		runner *fakeRunner
		want   BeadsHealth
	}{
		{
			name: "supported contract tolerates unknown fields",
			runner: &fakeRunner{runs: []fakeRun{
				{out: versionResult("1.1.7")},
				{out: `{"schema_version":1,"data":{"summary":{"total_issues":0},"future":true}}`},
			}},
			want: BeadsHealth{Usable: true, Version: "1.1.7", ContractVersion: 1},
		},
		{
			name:   "old version",
			runner: &fakeRunner{runs: []fakeRun{{out: versionResult("1.0.9")}}},
			want:   BeadsHealth{Reason: "beads_version_unsupported", Message: "Beads 1.0.9 is unsupported; install version >=1.1.0 and <1.2.0."},
		},
		{
			name:   "new version",
			runner: &fakeRunner{runs: []fakeRun{{out: versionResult("1.2.0")}}},
			want:   BeadsHealth{Reason: "beads_version_unsupported", Message: "Beads 1.2.0 is unsupported; install version >=1.1.0 and <1.2.0."},
		},
		{
			name: "future contract",
			runner: &fakeRunner{runs: []fakeRun{
				{out: versionEnvelope},
				{out: `{"schema_version":2,"data":{"summary":{"total_issues":0}}}`},
			}},
			want: BeadsHealth{Version: "1.1.0", Reason: "beads_contract_unsupported", Message: "Beads JSON contract 2 is unsupported; ocman requires contract 1."},
		},
		{
			name: "malformed contract",
			runner: &fakeRunner{runs: []fakeRun{
				{out: versionEnvelope},
				{out: `{"schema_version":1,"data":null}`},
			}},
			want: BeadsHealth{Version: "1.1.0", Reason: "beads_contract_unsupported", Message: "Beads returned an unsupported JSON contract; ocman requires contract 1."},
		},
		{
			name:   "missing executable",
			runner: &fakeRunner{pathErr: errors.New("missing")},
			want:   BeadsHealth{Reason: "beads_not_found", Message: "Beads is not installed; install bd version >=1.1.0 and <1.2.0."},
		},
		{
			name:   "version command failure",
			runner: &fakeRunner{runs: []fakeRun{{err: errors.New("timeout")}}},
			want:   BeadsHealth{Reason: "beads_command_failed", Message: "Beads version check failed; verify that bd can run as the ocman user."},
		},
		{
			name:   "invalid version output",
			runner: &fakeRunner{runs: []fakeRun{{out: versionResult("1.1")}}},
			want:   BeadsHealth{Reason: "beads_version_invalid", Message: "Beads returned an invalid version; ocman requires version >=1.1.0 and <1.2.0."},
		},
		{
			name: "store failure",
			runner: &fakeRunner{runs: []fakeRun{
				{out: versionEnvelope},
				{err: errors.New("unavailable")},
			}},
			want: BeadsHealth{Version: "1.1.0", Reason: "beads_store_unavailable", Message: "Factory Beads store is unavailable; verify its data directory and run bd status."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := probeBeads(context.Background(), filepath.Join(t.TempDir(), "beads"), tt.runner)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestProbeBeadsUsesPinnedReadOnlyContract(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "beads")
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`},
	}}

	got := probeBeads(context.Background(), dir, runner)
	if !got.Usable {
		t.Fatalf("probe = %#v", got)
	}
	wantCommands := [][]string{{"version", "--json"}, {"--readonly", "status", "--no-activity", "--json"}}
	if !reflect.DeepEqual(runner.seen, wantCommands) {
		t.Fatalf("commands = %v, want %v", runner.seen, wantCommands)
	}
	wantEnv := []string{"BD_JSON_ENVELOPE=1", "BEADS_DIR=" + dir, "BEADS_DB=", "BD_DB=", "BD_NON_INTERACTIVE=1", "BEADS_ACTOR=ocman-factory"}
	if !reflect.DeepEqual(runner.envs[1], wantEnv) {
		t.Fatalf("status env = %v, want %v", runner.envs[1], wantEnv)
	}
}

func TestOnlyOneServiceOwnsDispatch(t *testing.T) {
	dir := t.TempDir()
	beads := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {},
		{out: versionEnvelope}, {out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`}, {out: listEnvelope(`[]`)},
		{out: versionEnvelope}, {out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`}, {out: listEnvelope(`[]`)},
	}}
	first := newWithRunner(dir, beads, nil)
	second := newWithRunner(dir, beads, nil)
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)

	if got := first.Status(context.Background()); !got.DispatchOwner || got.ReadOnly || got.Health != HealthHealthy || !got.Idle {
		t.Fatalf("first status = %#v", got)
	}
	if got := second.Status(context.Background()); got.DispatchOwner || !got.ReadOnly || got.Health != HealthHealthy || !got.Idle {
		t.Fatalf("second status = %#v", got)
	}
	first.Close()
	third := newWithRunner(dir, &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {}}}, nil)
	if err := third.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(third.Close)
	if !third.owned {
		t.Fatal("dispatch lock was not released")
	}
}

func TestServiceInitializesFactoryStoreBeforeReportingStatus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "factory")
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{},
		{out: versionEnvelope},
		{out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`},
		{out: listEnvelope(`[]`)},
	}}
	svc := newWithRunner(dir, runner, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if got := svc.Status(context.Background()); got.Health != HealthHealthy || !got.Idle {
		t.Fatalf("status = %#v", got)
	}
	wantInit := []string{"init", "--quiet", "--stealth", "--skip-agents", "--skip-hooks", "--non-interactive", "--init-if-missing"}
	if !reflect.DeepEqual(runner.seen[1], wantInit) {
		t.Fatalf("init command = %v, want %v", runner.seen[1], wantInit)
	}
	if !slices.Contains(runner.envs[1], "BEADS_DIR="+filepath.Join(dir, "beads")) {
		t.Fatalf("init env = %v", runner.envs[1])
	}
}

func TestServiceReportsLockAndStoreFailures(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := newWithRunner(blocked, &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`},
	}}, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := svc.Status(context.Background()); got.Health != HealthDegraded || got.Reason != "dispatch_lock_failed" || !got.ReadOnly {
		t.Fatalf("status = %#v", got)
	}
}

func TestServiceMapsBeadsFailuresToHealth(t *testing.T) {
	for _, tt := range []struct {
		name string
		runs []fakeRun
		want Health
	}{
		{name: "unsupported is unavailable", runs: []fakeRun{{out: versionResult("1.2.0")}, {out: versionResult("1.2.0")}}, want: HealthUnavailable},
		{name: "store outage is degraded", runs: []fakeRun{{out: versionEnvelope}, {}, {out: versionEnvelope}, {err: errors.New("down")}}, want: HealthDegraded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newWithRunner(t.TempDir(), &fakeRunner{runs: tt.runs}, nil)
			if err := svc.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(svc.Close)
			if got := svc.Status(context.Background()); got.Health != tt.want || got.Idle {
				t.Fatalf("status = %#v, want health %q", got, tt.want)
			}
		})
	}
}

func TestLimitedBufferBoundsOutput(t *testing.T) {
	buffer := limitedBuffer{remaining: 3}
	if n, err := buffer.Write([]byte("abcdef")); err != nil || n != 6 || buffer.String() != "abc" || !buffer.overflow {
		t.Fatalf("write = %d, %v; buffer=%q overflow=%v", n, err, buffer.String(), buffer.overflow)
	}
}

func TestExecRunnerHonorsCancellation(t *testing.T) {
	printf, err := exec.LookPath("printf")
	if err != nil {
		t.Skip("printf is unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = (execRunner{}).Run(ctx, printf, t.TempDir(), []string{"x"}, nil)
	if err == nil {
		t.Fatal("cancelled command unexpectedly succeeded")
	}
}

func TestCreateWorkEpicValidatesBeforeBeads(t *testing.T) {
	runner := &fakeRunner{}
	svc := newWithRunner(t.TempDir(), runner, nil)
	validProject := t.TempDir()
	for _, tt := range []struct {
		name string
		req  CreateWorkEpicRequest
	}{
		{name: "acknowledgement", req: CreateWorkEpicRequest{InstantiationID: "intake-1", Goal: "Ship it", InitialProject: validProject}},
		{name: "goal", req: CreateWorkEpicRequest{InstantiationID: "intake-1", InitialProject: validProject, AcknowledgeLocalExecution: true}},
		{name: "instantiation ID", req: CreateWorkEpicRequest{Goal: "Ship it", InitialProject: validProject, AcknowledgeLocalExecution: true}},
		{name: "stable instantiation ID", req: CreateWorkEpicRequest{InstantiationID: "not stable!", Goal: "Ship it", InitialProject: validProject, AcknowledgeLocalExecution: true}},
		{name: "absolute local project", req: CreateWorkEpicRequest{InstantiationID: "intake-1", Goal: "Ship it", InitialProject: "relative", AcknowledgeLocalExecution: true}},
		{name: "existing local project", req: CreateWorkEpicRequest{InstantiationID: "intake-1", Goal: "Ship it", InitialProject: filepath.Join(validProject, "missing"), AcknowledgeLocalExecution: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.CreateWorkEpic(context.Background(), tt.req); err == nil {
				t.Fatal("CreateWorkEpic unexpectedly succeeded")
			}
		})
	}
	if len(runner.seen) != 0 {
		t.Fatalf("validation invoked Beads: %v", runner.seen)
	}
}

func TestPrepareWorkCanonicalizesGitRepoAndScopesAcknowledgement(t *testing.T) {
	repo := initTestRepo(t)
	subdir := filepath.Join(repo, "nested")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &fakeAckStore{}
	svc := newWithRunner(t.TempDir(), &fakeRunner{}, store)
	svc.projects = fakeProjectResolver{root: repo}
	svc.owned = true
	req := PrepareWorkRequest{Goal: "Ship search", Brief: "## Constraints\n\n- Keep the API stable.", ProjectPath: subdir}

	prepared, err := svc.PrepareWork(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(repo)
	if prepared.ProjectPath != canonical || !prepared.AcknowledgementRequired || prepared.Formula.ID != DefaultFormulaID || prepared.Formula.Version != DefaultFormulaVersion || prepared.PreparationKey == "" {
		t.Fatalf("prepared work = %#v", prepared)
	}
	store.has = true
	again, err := svc.PrepareWork(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if again.PreparationKey != prepared.PreparationKey || again.AcknowledgementRequired {
		t.Fatalf("second preparation = %#v", again)
	}
	wantCheck := []string{"local", canonical, "factory-plan", "v1"}
	if len(store.checks) != 2 || !reflect.DeepEqual(store.checks[0], wantCheck) {
		t.Fatalf("acknowledgement checks = %#v", store.checks)
	}
}

func TestAcknowledgeLocalExecutionUsesCanonicalProjectAndCurrentProfile(t *testing.T) {
	repo := initTestRepo(t)
	store := &fakeAckStore{}
	svc := newWithRunner(t.TempDir(), &fakeRunner{}, store)
	svc.projects = fakeProjectResolver{root: repo}
	svc.owned = true

	if err := svc.AcknowledgeLocalExecution(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(repo)
	if len(store.calls) != 1 || !reflect.DeepEqual(store.calls[0][:5], []any{"local", canonical, "factory-plan", "v1", "operator"}) {
		t.Fatalf("acknowledgement = %#v", store.calls)
	}
}

func TestCreatePreparedWorkEpicRequiresAckAndPersistsConfirmedBrief(t *testing.T) {
	repo := initTestRepo(t)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-1","planning":"fac-1.1","approval":"fac-1.2"}}}`},
	}}
	store := &fakeAckStore{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.projects = fakeProjectResolver{root: repo}
	svc.owned = true
	prepared, err := svc.PrepareWork(context.Background(), PrepareWorkRequest{
		Goal: "Ship search", Brief: "## Constraints\n\n- Keep the API stable.", ProjectPath: repo,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreatePreparedWorkEpic(context.Background(), prepared); !errors.Is(err, ErrAcknowledgementRequired) {
		t.Fatalf("CreatePreparedWorkEpic error = %v, want acknowledgement required", err)
	}
	if len(runner.seen) != 0 {
		t.Fatalf("unacknowledged create accessed Factory storage: %v", runner.seen)
	}
	store.has = true
	epic, err := svc.CreatePreparedWorkEpic(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if epic.Brief != prepared.Brief || epic.InstantiationID != prepared.PreparationKey {
		t.Fatalf("epic = %#v", epic)
	}
	var graph struct {
		Nodes []struct {
			Key         string `json:"key"`
			Description string `json:"description"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(runner.plans[0], &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 6 || graph.Nodes[0].Description != prepared.Brief || graph.Nodes[1].Description != prepared.Brief {
		t.Fatalf("graph brief = %#v", graph.Nodes)
	}

	stale := prepared
	stale.Brief += "\n- Changed after confirmation."
	if _, err := svc.CreatePreparedWorkEpic(context.Background(), stale); !errors.Is(err, ErrPreparationStale) {
		t.Fatalf("stale CreatePreparedWorkEpic error = %v", err)
	}
}

func TestCreatePreparedWorkEpicRetryReturnsOriginalEpic(t *testing.T) {
	repo := initTestRepo(t)
	store := &fakeAckStore{has: true}
	runner := &fakeRunner{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.projects = fakeProjectResolver{root: repo}
	svc.owned = true
	prepared, err := svc.PrepareWork(context.Background(), PrepareWorkRequest{
		Goal: "Ship search", Brief: "Confirmed brief", ProjectPath: repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.runs = []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-1","planning":"fac-1.1","approval":"fac-1.2"}}}`},
		{out: versionEnvelope}, {out: listEnvelope(pouredPreparedIssues(prepared.ProjectPath, prepared.PreparationKey, prepared.Brief))},
	}

	if _, err := svc.CreatePreparedWorkEpic(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	got, err := svc.CreatePreparedWorkEpic(context.Background(), prepared)
	if err != nil || got.ID != "fac-1" || got.Brief != prepared.Brief {
		t.Fatalf("retry = %#v, %v", got, err)
	}
	creates := 0
	for _, args := range runner.seen {
		if len(args) > 0 && args[0] == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("create calls = %d", creates)
	}
}

func TestCreateWorkEpicStopsWhenAcknowledgementCannotBePersisted(t *testing.T) {
	runner := &fakeRunner{}
	store := &fakeAckStore{runner: runner, err: errors.New("disk full")}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true
	_, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID: "intake-1", Goal: "Ship it", InitialProject: t.TempDir(), AcknowledgeLocalExecution: true,
	})
	if !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("CreateWorkEpic error = %v, want factory unavailable", err)
	}
	if len(runner.seen) != 0 {
		t.Fatalf("failed acknowledgement invoked Beads: %v", runner.seen)
	}
}

func TestReadOnlyServiceCannotCreateWorkEpic(t *testing.T) {
	project := t.TempDir()
	runner := &fakeRunner{pathErr: errors.New("Beads must not be called")}
	store := &fakeAckStore{runner: runner}
	svc := newWithRunner(t.TempDir(), runner, store)

	_, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID: "intake-1", Goal: "Ship it", InitialProject: project, AcknowledgeLocalExecution: true,
	})
	if !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("CreateWorkEpic error = %v, want factory unavailable", err)
	}
	if len(store.calls) != 0 || len(runner.seen) != 0 {
		t.Fatalf("read-only create wrote state: acknowledgements=%d Beads=%v", len(store.calls), runner.seen)
	}
}

func TestRecoveryFailurePreventsCreatingWorkEpic(t *testing.T) {
	project, _ := filepath.EvalSymlinks(t.TempDir())
	runner := &fakeRunner{runs: []fakeRun{{err: errors.New("still unavailable")}}}
	store := &fakeFactoryStore{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true
	svc.recoveryErr = errors.New("recovery failed")

	_, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID: "intake-1", Goal: "Ship it", InitialProject: project, AcknowledgeLocalExecution: true,
	})
	if !errors.Is(err, ErrFactoryUnavailable) || len(store.calls) != 0 || len(runner.seen) != 1 || runner.seen[0][0] != "version" {
		t.Fatalf("CreateWorkEpic error = %v, acknowledgements = %#v, commands = %#v", err, store.calls, runner.seen)
	}
}

func TestDefaultFormulaIsImmutable(t *testing.T) {
	formula := DefaultFormula()
	formula.ID = "changed"
	formula.Parameters[0].Name = "changed"

	got := DefaultFormula()
	if got.ID != DefaultFormulaID || got.Version != DefaultFormulaVersion || got.Parameters[0] != (FormulaParameter{Name: "goal", Type: "string"}) {
		t.Fatalf("DefaultFormula = %#v", got)
	}
}

func TestCreateWorkEpicPoursDefaultFormulaGraph(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-1","planning":"fac-1.1","approval":"fac-1.2"}}}`},
	}}
	store := &fakeFactoryStore{fakeAckStore: fakeAckStore{runner: runner}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true

	got, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID:           "intake-42",
		Goal:                      "Ship search",
		InitialProject:            project,
		AcknowledgeLocalExecution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fac-1" || got.Planning.WorkID != "fac-1.1" || got.Planning.ApprovalGateID != "fac-1.2" {
		t.Fatalf("epic = %#v", got)
	}
	if len(store.calls) != 1 || !reflect.DeepEqual(store.calls[0][:5], []any{"local", project, "factory-plan", "v1", "operator"}) {
		t.Fatalf("acknowledgement = %#v", store.calls)
	}
	if len(runner.plans) != 1 {
		t.Fatalf("plans = %d", len(runner.plans))
	}
	if strings.Contains(string(runner.plans[0]), "acknowledg") || strings.Contains(string(runner.plans[0]), `"operator"`) {
		t.Fatalf("acknowledgement leaked into Beads graph: %s", runner.plans[0])
	}
	var plan struct {
		Nodes []struct {
			Key          string            `json:"key"`
			Title        string            `json:"title"`
			Type         string            `json:"type"`
			Description  string            `json:"description"`
			ParentKey    string            `json:"parent_key"`
			Metadata     map[string]string `json:"metadata"`
			MetadataRefs map[string]string `json:"metadata_refs"`
		} `json:"nodes"`
		Edges []struct {
			From string `json:"from_key"`
			To   string `json:"to_key"`
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(runner.plans[0], &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 6 || len(plan.Edges) != 4 {
		t.Fatalf("graph = %#v", plan)
	}
	wantKinds := []string{"work-epic", "agent-work", "gate", "delivery", "gate", "gate"}
	for i, node := range plan.Nodes {
		if node.Metadata["ocman.contract"] != "1" || node.Metadata["ocman.kind"] != wantKinds[i] ||
			node.Metadata["ocman.formula_id"] != DefaultFormulaID || node.Metadata["ocman.formula_version"] != strconv.Itoa(DefaultFormulaVersion) ||
			node.Metadata["ocman.formula_origin"] != "built-in" || node.Metadata["ocman.instantiation_id"] != "intake-42" {
			t.Fatalf("node %q provenance = %#v", node.Key, node.Metadata)
		}
	}
	if plan.Nodes[1].ParentKey != "epic" || plan.Nodes[1].Metadata["ocman.permission_profile"] != "factory-plan/v1" ||
		plan.Nodes[1].Description != "Plan delivery for "+project ||
		plan.Nodes[2].ParentKey != "epic" || plan.Nodes[2].Title != "Plan approval" ||
		plan.Edges[0].From != "approval" || plan.Edges[0].To != "planning" || plan.Edges[0].Type != "blocks" {
		t.Fatalf("graph relationships = %#v", plan)
	}
	wantCommands := [][]string{
		{"version", "--json"},
		{"--readonly", "list", "--all", "--include-gates", "--limit", "0", "--metadata-field", "ocman.contract=1", "--json"},
	}
	if !reflect.DeepEqual(runner.seen[:2], wantCommands) || len(runner.seen[2]) != 4 || runner.seen[2][0] != "create" || runner.seen[2][1] != "--graph" || runner.seen[2][3] != "--json" || !filepath.IsAbs(runner.seen[2][2]) {
		t.Fatalf("commands = %v", runner.seen)
	}
	for _, env := range runner.envs {
		if !slices.Contains(env, "BD_JSON_ENVELOPE=1") || !slices.Contains(env, "BEADS_ACTOR=ocman-factory") ||
			!slices.Contains(env, "BEADS_DB=") || !slices.Contains(env, "BD_DB=") {
			t.Fatalf("unsafe env = %v", env)
		}
	}
}

func TestCreateWorkEpicReconcilesAmbiguousPour(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{out: listEnvelope(`[]`)},
		{err: errors.New("connection lost after commit")},
		{out: listEnvelope(currentPouredIssuesAt(project))},
	}}
	svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{fakeAckStore: fakeAckStore{runner: runner}})
	svc.owned = true

	got, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{InstantiationID: "intake-42", Goal: "Ship search", InitialProject: project, AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fac-1" || got.Planning.WorkStatus != "in_progress" || got.Planning.ApprovalStatus != "open" {
		t.Fatalf("reconciled epic = %#v", got)
	}
}

func TestCreateWorkEpicReconcilesAfterRequestCancellation(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{out: listEnvelope(`[]`)},
		{err: errors.New("connection lost after commit")},
		{out: listEnvelope(currentPouredIssuesAt(project))},
	}}
	createSeen, reconciliationBounded := false, false
	runner.onRun = func(runCtx context.Context, args []string) {
		if len(args) != 0 && args[0] == "create" {
			createSeen = true
			cancel()
			return
		}
		if createSeen {
			deadline, ok := runCtx.Deadline()
			if !ok || time.Until(deadline) > beadsTimeout {
				t.Fatalf("reconciliation context deadline = %v, %v", deadline, ok)
			}
			reconciliationBounded = true
		}
	}
	svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{fakeAckStore: fakeAckStore{runner: runner}})
	svc.owned = true

	got, err := svc.CreateWorkEpic(ctx, CreateWorkEpicRequest{InstantiationID: "intake-42", Goal: "Ship search", InitialProject: project, AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fac-1" || len(runner.seen) != 4 || !reconciliationBounded {
		t.Fatalf("reconciled epic = %#v; commands = %v", got, runner.seen)
	}
}

func TestCreateWorkEpicDoesNotDuplicateInstantiation(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-1","planning":"fac-1.1","approval":"fac-1.2"}}}`},
		{out: versionEnvelope}, {out: listEnvelope(currentPouredIssuesAt(project))},
	}}
	svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{fakeAckStore: fakeAckStore{runner: runner}})
	svc.owned = true
	req := CreateWorkEpicRequest{InstantiationID: "intake-42", Goal: "Ship search", InitialProject: project, AcknowledgeLocalExecution: true}
	if _, err := svc.CreateWorkEpic(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.CreateWorkEpic(context.Background(), req); err != nil || got.ID != "fac-1" {
		t.Fatalf("second create = %#v, %v", got, err)
	}
	creates := 0
	for _, args := range runner.seen {
		if len(args) > 0 && args[0] == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("create calls = %d; commands = %v", creates, runner.seen)
	}
}

func TestCreateWorkEpicRejectsInstantiationInputMismatch(t *testing.T) {
	_, err := matchInstantiation([]WorkEpic{{
		InstantiationID: "intake-42", FormulaID: DefaultFormulaID, FormulaVersion: DefaultFormulaVersion,
		FormulaRevision: DefaultFormulaVersion,
		Goal:            "Original", InitialProject: "/repo",
	}}, CreateWorkEpicRequest{InstantiationID: "intake-42", Goal: "Changed", InitialProject: "/repo", FormulaID: DefaultFormulaID, FormulaRevision: DefaultFormulaVersion}, "")
	if !errors.Is(err, ErrInstantiationConflict) {
		t.Fatalf("matchInstantiation error = %v, want conflict", err)
	}
}

func TestCreateWorkEpicRejectsInstantiationFormulaMismatch(t *testing.T) {
	_, err := matchInstantiation([]WorkEpic{{
		InstantiationID: "intake-42", FormulaID: "custom/other", FormulaRevision: 1, FormulaHash: strings.Repeat("a", 64),
	}}, CreateWorkEpicRequest{InstantiationID: "intake-42", FormulaID: "custom/team", FormulaRevision: 1}, strings.Repeat("b", 64))
	if !errors.Is(err, ErrInstantiationConflict) {
		t.Fatalf("matchInstantiation error = %v, want provenance conflict", err)
	}
}

func TestServiceListsGetsAndCountsWorkEpics(t *testing.T) {
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(pouredIssues)},
		{out: versionEnvelope}, {out: listEnvelope(pouredIssues)},
		{out: versionEnvelope}, {out: `{"schema_version":1,"data":{"summary":{"total_issues":3}}}`}, {out: listEnvelope(pouredIssues)},
	}}
	svc := newWithRunner(t.TempDir(), runner, nil)

	epics, err := svc.ListWorkEpics(context.Background())
	if err != nil || len(epics) != 1 || epics[0].Goal != "Ship search" || epics[0].InitialProject != "/repo" {
		t.Fatalf("ListWorkEpics = %#v, %v", epics, err)
	}
	epic, err := svc.GetWorkEpic(context.Background(), "fac-1")
	if err != nil || epic.Planning.WorkID != "fac-1.1" || epic.Planning.ApprovalGateID != "fac-1.2" {
		t.Fatalf("GetWorkEpic = %#v, %v", epic, err)
	}
	status := svc.Status(context.Background())
	if status.Health != HealthHealthy || status.WorkEpicCount != 1 {
		t.Fatalf("Status = %#v", status)
	}
}
