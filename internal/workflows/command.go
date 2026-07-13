package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

const maxCommandOutput = 64 << 10

type CommandRequest struct {
	Directory   string
	Command     []string
	Environment map[string]string
	Permission  []PermissionRule
	Outputs     []Collector
}

type CommandResult struct {
	State           string
	ExitCode        int
	Stdout          string
	Stderr          string
	Error           string
	Outputs         map[string]string
	StdoutTruncated bool
	StderrTruncated bool
}

type CommandExecutor interface {
	Execute(context.Context, CommandRequest) CommandResult
}

type localCommandExecutor struct{}

func (localCommandExecutor) Execute(ctx context.Context, req CommandRequest) CommandResult {
	result := CommandResult{ExitCode: -1, Outputs: map[string]string{}}
	commandText := strings.Join(req.Command, " ")
	if !commandAllowed(commandText, req.Permission) {
		result.State = AttemptDenied
		result.Error = "permission denied for command: " + commandText
		return result
	}
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	cmd.Dir = req.Directory
	cmd.Env = commandEnv(req.Environment)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	result.StdoutTruncated, result.StderrTruncated = stdout.truncated, stderr.truncated
	if ctx.Err() != nil {
		result.State = AttemptCanceled
		result.Error = ctx.Err().Error()
		return result
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.State = AttemptFailed
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.State = AttemptErrored
		}
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 0
	for _, collector := range req.Outputs {
		value, err := collectOutput(ctx, req.Directory, collector, result.Stdout)
		if err != nil {
			result.State = AttemptFailed
			result.Error = fmt.Sprintf("collecting %s: %v", collector.Name, err)
			return result
		}
		result.Outputs[collector.Name] = value
	}
	result.State = AttemptSuccessful
	return result
}

func commandAllowed(command string, rules []PermissionRule) bool {
	action := "deny"
	for _, rule := range rules {
		if rule.Permission == "bash" && globMatch(rule.Pattern, command) {
			action = rule.Action
		}
	}
	return action == "allow"
}

func globMatch(pattern, value string) bool {
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, `\*`, `.*`)
	quoted = strings.ReplaceAll(quoted, `\?`, `.`)
	matched, _ := regexp.MatchString("^(?:"+quoted+")$", value)
	return matched
}

func commandEnv(environment map[string]string) []string {
	out := make([]string, 0, len(environment)+1)
	if path := os.Getenv("PATH"); path != "" {
		out = append(out, "PATH="+path)
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+environment[key])
	}
	return out
}

func collectOutput(ctx context.Context, directory string, collector Collector, stdout string) (string, error) {
	switch collector.Type {
	case "text":
		return stdout, nil
	case "file", "json_file":
		path, err := scopedPath(directory, collector.Path)
		if err != nil {
			return "", err
		}
		value, err := readBounded(path)
		if err != nil {
			return "", err
		}
		if collector.Type == "json_file" && !json.Valid([]byte(value)) {
			return "", fmt.Errorf("invalid JSON in %s", collector.Path)
		}
		return value, nil
	case "git_diff":
		cmd := gitexec.Command(ctx, "-C", directory, "diff", "--no-ext-diff", "--no-textconv", "--")
		output := &boundedBuffer{}
		cmd.Stdout = output
		stderr := &boundedBuffer{}
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return output.String(), nil
	default:
		return "", fmt.Errorf("unsupported collector type %q", collector.Type)
	}
}

func scopedPath(directory, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("collector path must be relative")
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("collector path escapes workflow directory")
	}
	return path, nil
}

func readBounded(path string) (string, error) {
	expected, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(expected, opened) {
		return "", fmt.Errorf("collector path changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxCommandOutput+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxCommandOutput {
		return "", fmt.Errorf("output exceeds %d bytes", maxCommandOutput)
	}
	return string(raw), nil
}

type boundedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	remaining := maxCommandOutput - b.buf.Len()
	if remaining > 0 {
		_, _ = b.buf.Write(value[:min(len(value), remaining)])
	}
	if len(value) > remaining {
		b.truncated = true
	}
	return len(value), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }
