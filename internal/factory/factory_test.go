package factory

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

type fakeRunner struct {
	pathErr error
	runs    []fakeRun
	seen    [][]string
	envs    [][]string
}

type fakeRun struct {
	out string
	err error
}

func (f *fakeRunner) LookPath(string) (string, error) { return "/usr/bin/bd", f.pathErr }

func (f *fakeRunner) Run(_ context.Context, _ string, _ string, args, env []string) ([]byte, []byte, error) {
	f.seen = append(f.seen, append([]string(nil), args...))
	f.envs = append(f.envs, append([]string(nil), env...))
	run := f.runs[len(f.seen)-1]
	return []byte(run.out), nil, run.err
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
				{out: `{"version":"1.1.7","future":"ignored"}`},
				{out: `{"schema_version":1,"data":{"summary":{"total_issues":0},"future":true}}`},
			}},
			want: BeadsHealth{Usable: true, Version: "1.1.7", ContractVersion: 1},
		},
		{
			name:   "old version",
			runner: &fakeRunner{runs: []fakeRun{{out: `{"version":"1.0.9"}`}}},
			want:   BeadsHealth{Reason: "beads_version_unsupported", Message: "Beads 1.0.9 is unsupported; install version >=1.1.0 and <1.2.0."},
		},
		{
			name:   "new version",
			runner: &fakeRunner{runs: []fakeRun{{out: `{"version":"1.2.0"}`}}},
			want:   BeadsHealth{Reason: "beads_version_unsupported", Message: "Beads 1.2.0 is unsupported; install version >=1.1.0 and <1.2.0."},
		},
		{
			name: "future contract",
			runner: &fakeRunner{runs: []fakeRun{
				{out: `{"version":"1.1.0"}`},
				{out: `{"schema_version":2,"data":{"summary":{"total_issues":0}}}`},
			}},
			want: BeadsHealth{Version: "1.1.0", Reason: "beads_contract_unsupported", Message: "Beads JSON contract 2 is unsupported; ocman requires contract 1."},
		},
		{
			name: "malformed contract",
			runner: &fakeRunner{runs: []fakeRun{
				{out: `{"version":"1.1.0"}`},
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
			runner: &fakeRunner{runs: []fakeRun{{out: `{"version":"1.1"}`}}},
			want:   BeadsHealth{Reason: "beads_version_invalid", Message: "Beads returned an invalid version; ocman requires version >=1.1.0 and <1.2.0."},
		},
		{
			name: "store failure",
			runner: &fakeRunner{runs: []fakeRun{
				{out: `{"version":"1.1.0"}`},
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
		{out: `{"version":"1.1.0"}`},
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
	wantEnv := []string{"BD_JSON_ENVELOPE=1", "BEADS_DIR=" + dir, "BEADS_DB=", "BD_DB="}
	if !reflect.DeepEqual(runner.envs[1], wantEnv) {
		t.Fatalf("status env = %v, want %v", runner.envs[1], wantEnv)
	}
}

func TestOnlyOneServiceOwnsDispatch(t *testing.T) {
	dir := t.TempDir()
	beads := &fakeRunner{runs: []fakeRun{
		{out: `{"version":"1.1.0"}`}, {},
		{out: `{"version":"1.1.0"}`}, {out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`},
		{out: `{"version":"1.1.0"}`}, {out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`},
	}}
	first := newWithRunner(dir, beads)
	second := newWithRunner(dir, beads)
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
	third := newWithRunner(dir, &fakeRunner{runs: []fakeRun{{out: `{"version":"1.1.0"}`}, {}}})
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
		{out: `{"version":"1.1.0"}`},
		{},
		{out: `{"version":"1.1.0"}`},
		{out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`},
	}}
	svc := newWithRunner(dir, runner)
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
		{out: `{"version":"1.1.0"}`},
		{out: `{"schema_version":1,"data":{"summary":{"total_issues":0}}}`},
	}})
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
		{name: "unsupported is unavailable", runs: []fakeRun{{out: `{"version":"1.2.0"}`}, {out: `{"version":"1.2.0"}`}}, want: HealthUnavailable},
		{name: "store outage is degraded", runs: []fakeRun{{out: `{"version":"1.1.0"}`}, {}, {out: `{"version":"1.1.0"}`}, {err: errors.New("down")}}, want: HealthDegraded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newWithRunner(t.TempDir(), &fakeRunner{runs: tt.runs})
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
