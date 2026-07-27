package workflows

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/safety"
)

const maxCommandOutput = 64 << 10

type CommandRequest struct {
	Directory   string
	Command     []string
	Environment map[string]string
	Permission  []PermissionRule
	// RestrictGit, when non-empty, names git subcommands the caller may
	// not run because it holds a path-scoped (non-exclusive) workspace
	// lease. A centralized coordinator owns repository-wide git mutation.
	RestrictGit []string
}

type CommandResult struct {
	State           string
	ExitCode        int
	Stdout          string
	Stderr          string
	Error           string
	StdoutTruncated bool
	StderrTruncated bool
}

type CommandExecutor interface {
	Execute(context.Context, CommandRequest) CommandResult
}

type localCommandExecutor struct{}

func (localCommandExecutor) Execute(ctx context.Context, req CommandRequest) CommandResult {
	result := CommandResult{ExitCode: -1}
	commandText := strings.Join(req.Command, " ")
	if reason := safety.Denied(commandText); reason != "" {
		result.State = AttemptDenied
		result.Error = "command blocked by the hard denylist (" + reason + "): " + commandText
		return result
	}
	if !commandAllowed(commandText, req.Permission) {
		result.State = AttemptDenied
		result.Error = "permission denied for command: " + commandText
		return result
	}
	if len(req.RestrictGit) > 0 && gitMutationDenied(req.Command, req.RestrictGit) {
		result.State = AttemptDenied
		result.Error = "path-leased node may not run repository-wide git mutation: " + commandText
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
	result.State = AttemptSuccessful
	return result
}

// commandAllowed gates a workflow command node. The permission rules
// come from the workflow definition, which an agent can publish, so a
// definition carrying `{"permission":"bash","pattern":"*","action":
// "allow"}` would otherwise authorise anything. The shared hard
// denylist is checked first and no rule can override it.
func commandAllowed(command string, rules []PermissionRule) bool {
	if safety.Denied(command) != "" {
		return false
	}
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
