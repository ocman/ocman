package claudeprovider

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ocman/ocman/internal/platforms/claude/launch"
)

func prepareConnectionOptions(opts Options) (Options, error) {
	if !opts.ConnectionAccount {
		return opts, nil
	}
	if !filepath.IsAbs(opts.ConfigDir) {
		return opts, fmt.Errorf("named Claude account requires an explicit absolute configuration directory")
	}
	config, err := filepath.EvalSymlinks(opts.ConfigDir)
	if err != nil {
		return opts, fmt.Errorf("named Claude account unavailable: %w", err)
	}
	info, err := os.Stat(config)
	if err != nil || !info.IsDir() {
		return opts, fmt.Errorf("named Claude account configuration is not a directory")
	}
	// Freeze the resolved directory; a caller's ConfigEnv pointer must not
	// retarget execution independently of history and ownership checks.
	opts.ConfigDir, opts.ConfigEnv = config, new(config)
	return opts, nil
}

func (o Options) sessionEnv(directory string) []string {
	if o.ConnectionAccount {
		return claudecli.AccountEnvironment(o.ConfigDir, directory)
	}
	return claudecli.Environment(o.ConfigEnv, directory)
}
