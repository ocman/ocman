package tmux

import "context"

// ManagedServer isolates OpenCode from the user's tmux server and configuration.
const ManagedServer = "ocman-opencode"

func managedArgs(args ...string) []string {
	return append([]string{"-L", ManagedServer, "-f", "/dev/null"}, args...)
}

// ManagedRunner controls only the dedicated headless OpenCode server.
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
	KillSession: func(ctx context.Context, name string) error {
		return runCommand(ctx, commandTimeout, managedArgs("kill-session", "-t", name)...)
	},
}
