package claudeprovider

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	claudecli "github.com/ocman/ocman/internal/platforms/claude/launch"
)

// Account plugin operations preserve Claude's user-scope CLI behavior while
// selecting the explicit configuration and stripping inherited owner auth.
func InstallAccountPlugin(ctx context.Context, cli, config, marketplace string) error {
	return installPlugin(ctx, cli, marketplace, accountPluginRunner(config))
}

func EnsureAccountPlugin(ctx context.Context, cli, config, marketplace string) (string, error) {
	return ensurePlugin(ctx, cli, config, marketplace, accountPluginRunner(config))
}

func UninstallAccountPlugin(ctx context.Context, cli, config string) error {
	return uninstallPlugin(ctx, cli, accountPluginRunner(config))
}

func accountPluginRunner(config string) pluginRunner {
	return func(ctx context.Context, cli string, args ...string) error {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, append([]string{"--setting-sources", "user"}, args...)...)
		cmd.Env = claudecli.AccountEnvironment(config, "")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("claude %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}
