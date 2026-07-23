package dagu

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

type Status string

const (
	Unavailable Status = "unavailable"
	Compatible  Status = "compatible"
	Unsupported Status = "unsupported"
)

type Result struct {
	Status         Status `json:"status"`
	Version        string `json:"version,omitempty"`
	InstallCommand string `json:"installCommand"`
}

type Runner interface {
	LookPath(name string) (string, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type osRunner struct{}

func (osRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (osRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

type Detector struct {
	runner Runner
	goos   string
}

func NewDetector(runner Runner, goos string) Detector { return Detector{runner: runner, goos: goos} }

func Detect(ctx context.Context) Result { return NewDetector(osRunner{}, runtime.GOOS).Status(ctx) }

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])(\d+\.\d+\.\d+)(?:[^0-9]|$)`)

func (d Detector) Status(ctx context.Context) Result {
	result := Result{Status: Unavailable, InstallCommand: installCommand(d.goos)}
	path, err := d.runner.LookPath("dagu")
	if err != nil {
		return result
	}
	output, err := d.runner.Output(ctx, path, "version")
	if err != nil {
		result.Status = Unsupported
		return result
	}
	match := versionPattern.FindStringSubmatch(string(output))
	if len(match) != 2 {
		result.Status = Unsupported
		return result
	}
	result.Version = match[1]
	result.Status = Unsupported
	if strings.HasPrefix(result.Version, "2.") {
		result.Status = Compatible
	}
	return result
}

func installCommand(goos string) string {
	switch goos {
	case "darwin":
		return "brew install dagu"
	case "linux":
		return "curl -fsSL https://raw.githubusercontent.com/dagucloud/dagu/main/scripts/installer.sh | bash"
	default:
		return "npm install -g --ignore-scripts=false @dagucloud/dagu"
	}
}
