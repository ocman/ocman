package dagu

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	path   string
	lookup error
	output string
	run    error
	calls  int
}

func (f *fakeRunner) LookPath(string) (string, error) { return f.path, f.lookup }
func (f *fakeRunner) Output(context.Context, string, ...string) ([]byte, error) {
	f.calls++
	return []byte(f.output), f.run
}

func TestDetectorStatus(t *testing.T) {
	tests := []struct {
		name    string
		runner  *fakeRunner
		want    Status
		version string
		calls   int
	}{
		{name: "unavailable", runner: &fakeRunner{lookup: errors.New("missing")}, want: Unavailable},
		{name: "compatible", runner: &fakeRunner{path: "/bin/dagu", output: "dagu version v2.1.0\n"}, want: Compatible, version: "2.1.0", calls: 1},
		{name: "old major", runner: &fakeRunner{path: "/bin/dagu", output: "1.15.0"}, want: Unsupported, version: "1.15.0", calls: 1},
		{name: "future major", runner: &fakeRunner{path: "/bin/dagu", output: "dagu 3.0.0"}, want: Unsupported, version: "3.0.0", calls: 1},
		{name: "bad output", runner: &fakeRunner{path: "/bin/dagu", output: "unknown"}, want: Unsupported, calls: 1},
		{name: "command failure", runner: &fakeRunner{path: "/bin/dagu", run: errors.New("failed")}, want: Unsupported, calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewDetector(tt.runner, "linux").Status(context.Background())
			if got.Status != tt.want || got.Version != tt.version || tt.runner.calls != tt.calls {
				t.Fatalf("Status() = %+v, calls=%d", got, tt.runner.calls)
			}
			if got.InstallCommand != "curl -fsSL https://raw.githubusercontent.com/dagucloud/dagu/main/scripts/installer.sh | bash" {
				t.Fatalf("InstallCommand = %q", got.InstallCommand)
			}
		})
	}
}

func TestInstallCommand(t *testing.T) {
	tests := map[string]string{
		"darwin":  "brew install dagu",
		"linux":   "curl -fsSL https://raw.githubusercontent.com/dagucloud/dagu/main/scripts/installer.sh | bash",
		"windows": "npm install -g --ignore-scripts=false @dagucloud/dagu",
	}
	for os, want := range tests {
		got := NewDetector(&fakeRunner{lookup: errors.New("missing")}, os).Status(context.Background())
		if got.InstallCommand != want {
			t.Errorf("%s InstallCommand = %q, want %q", os, got.InstallCommand, want)
		}
	}
}
