package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ocman/ocman/internal/configfile"
	"github.com/ocman/ocman/internal/state"
)

func defaultConfigPath() string {
	if path := os.Getenv("OCMAN_CONFIG"); path != "" {
		return path
	}
	// Explicit state isolation also isolates the auto-migrated config. Dev/test
	// instances must never migrate or edit the daily driver's configuration.
	if path := os.Getenv("OCMAN_STATE_DB"); path != "" {
		return filepath.Join(filepath.Dir(path), "config.yaml")
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ocman", "config.yaml")
}

// prepareFileConfig applies file and environment values before the normal flag
// parse, so explicit CLI flags retain their existing precedence and Visit state.
func prepareFileConfig(fs *flag.FlagSet, args []string, fallback string) (string, error) {
	arguments, err := parsedFlagArguments(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	path := fallback
	if selected, explicit := arguments["config"]; explicit {
		path = selected
	}
	if path == "" {
		return "", errors.New("configuration path cannot be empty")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	f := &configfile.File{Path: path}
	doc, err := f.Read()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if doc != nil {
		for _, name := range doc.Keys("startup") {
			if name == "config" || fs.Lookup(name) == nil {
				return "", errors.New("unknown startup configuration option")
			}
			value, _, err := doc.Get("startup", name)
			if err != nil {
				return "", err
			}
			if name == "auth-password" {
				if _, err := configfile.ParseSecret(value); err != nil {
					return "", err
				}
			}
			if err := fs.Set(name, value); err != nil {
				return "", errors.New("invalid startup configuration value")
			}
		}
	}
	// Keep this explicit: only supported deployment options gain env overrides.
	for name, env := range startupEnvironment {
		if value, ok := os.LookupEnv(env); ok {
			if err := fs.Set(name, value); err != nil {
				return "", fmt.Errorf("invalid %s override", env)
			}
		}
	}
	return path, nil
}

// Resolve credentials after CLI parsing, so an unused reference is never loaded.
// Environment values and explicit CLI passwords are literal credentials.
func resolveConfigAuth(fs *flag.FlagSet, args []string) error {
	password := fs.Lookup("auth-password")
	if password == nil || password.Value.String() == "" || authPasswordFromEnvironment() != "" || flagPresent(fs, args, "auth-password") {
		return nil
	}
	if file := fs.Lookup("auth-password-file"); file != nil && file.Value.String() != "" {
		return nil
	}
	resolver := configfile.NewResolver(func(ctx context.Context, id string) (string, error) {
		return state.ReadConfigSecret(ctx, state.DefaultDBPath(), id)
	})
	value, err := resolver.Resolve(context.Background(), password.Value.String())
	if err != nil {
		return fmt.Errorf("resolving startup authentication: %w", err)
	}
	return password.Value.Set(value)
}

var startupEnvironment = map[string]string{
	"addr":             "OCMAN_ADDR",
	"gui":              "OCMAN_GUI",
	"mcp-addr":         "OCMAN_MCP_ADDR",
	"platforms":        "OCMAN_PLATFORMS",
	"public-base-url":  "OCMAN_PUBLIC_BASE_URL",
	"relay-url":        "OCMAN_RELAY_URL",
	"conversations-db": "OCMAN_CONVERSATIONS_DB",
	"remote-listen":    "OCMAN_REMOTE_LISTEN",
}
