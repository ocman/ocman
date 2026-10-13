package tmux

import (
	"context"
	"errors"
	"os/exec"
)

// ManagedServer is the one tmux server hosting every agent runtime ocman
// launches (headless OpenCode and provider supervisors), isolated from the
// user's tmux server and configuration. List it with
// `tmux -L ocman-agents list-sessions`.
const ManagedServer = "ocman-agents"

// legacyServers hosted runtimes before they shared ManagedServer. Instances
// launched there survive an upgrade, so stop and liveness checks still find
// them; new launches never go there.
// ponytail: drop once no install runs a pre-ocman-agents instance.
var legacyServers = []string{"ocman-opencode", "ocman-provider"}

// ManagedArgs prefixes a tmux command for the managed server.
func ManagedArgs(args ...string) []string { return serverArgs(ManagedServer, args...) }

func managedArgs(args ...string) []string { return ManagedArgs(args...) }

func serverArgs(server string, args ...string) []string {
	return append([]string{"-L", server, "-f", "/dev/null"}, args...)
}

// KillManagedSession kills a session on the managed server, or on the legacy
// server that still hosts it. The managed server's error is reported when no
// server has the session.
func KillManagedSession(ctx context.Context, name string) error {
	err := runCommand(ctx, commandTimeout, managedArgs("kill-session", "-t", name)...)
	if err == nil {
		return nil
	}
	for _, server := range legacyServers {
		if runCommand(ctx, commandTimeout, serverArgs(server, "kill-session", "-t", name)...) == nil {
			return nil
		}
	}
	return err
}

// HasManagedSession reports whether any managed (or legacy) server has the
// session. tmux exits 1 for a missing session or server; other failures are
// errors.
func HasManagedSession(ctx context.Context, name string) (bool, error) {
	var firstErr error
	for _, server := range append([]string{ManagedServer}, legacyServers...) {
		_, err := outputCommand(ctx, serverArgs(server, "has-session", "-t", name)...)
		if err == nil {
			return true, nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			firstErr = errors.Join(firstErr, err)
		}
	}
	return false, firstErr
}

// ManagedRunner controls only the dedicated headless agent server.
var ManagedRunner = Runner{
	ListSessions: func(ctx context.Context) ([]Session, error) {
		return listSessions(ctx, managedArgs("list-sessions", "-F", "#{session_name}\t#{session_windows}")...)
	},
	NewSessionEnv: func(ctx context.Context, name, directory, command string, env map[string]string) error {
		args := []string{"new-session", "-d", "-s", name, "-c", directory}
		args = append(args, envArgs(env)...)
		args = append(args, command)
		return runCommand(ctx, commandTimeout, managedArgs(args...)...)
	},
	NewWindowEnv: func(ctx context.Context, name, directory, command string, env map[string]string) error {
		args := []string{"new-window", "-d", "-t", name, "-c", directory}
		args = append(args, envArgs(env)...)
		args = append(args, command)
		return runCommand(ctx, commandTimeout, managedArgs(args...)...)
	},
	KillSession: KillManagedSession,
}
