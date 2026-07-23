package dagu

import (
	"context"
	"io"
	"os"
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

type Process interface {
	Wait() error
	Kill() error
}

type ManagerRunner interface {
	Runner
	Start(name string, args, env []string) (Process, error)
}

type osRunner struct{}

func (osRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (osRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
func (osRunner) Start(name string, args, env []string) (Process, error) {
	cmd := exec.Command(name, args...)
	prepareProcess(cmd)
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return osProcess{cmd}, nil
}

type osProcess struct{ cmd *exec.Cmd }

func (p osProcess) Wait() error { return p.cmd.Wait() }
func (p osProcess) Kill() error { return killProcess(p.cmd) }

func processEnvironment(home string) []string {
	env := []string{"HOME=" + home, "DAGU_HOME=" + home, "DAGU_AUTH_MODE=none"}
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if name == "PATH" || name == "SHELL" || name == "TMPDIR" || name == "LANG" || name == "LC_ALL" || name == "TZ" || name == "USER" {
			env = append(env, value)
		}
	}
	return env
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
