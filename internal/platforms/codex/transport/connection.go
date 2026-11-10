package codextransport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/hostsvc/local"
)

// ConnectionConfig pins a named account to its owner-resolved Codex home.
// ID is the immutable cnx_<uuid> identity, never the display name or platform ID.
type ConnectionConfig struct {
	ID, Home string
}

type connectionPaths struct {
	home, data, root string
	named            bool
}

func resolvePlatformPaths(ctx context.Context, cfg Config) (connectionPaths, error) {
	if cfg.Connection != nil {
		return resolveNamedPaths(ctx, cfg)
	}
	home, err := codexHome()
	if err != nil {
		return connectionPaths{}, fmt.Errorf("codex home unavailable: %w", err)
	}
	root := filepath.Join(cfg.DataDir, "codex-supervisor")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return connectionPaths{}, err
	}
	root, err = local.ProviderRoot(ctx, root)
	return connectionPaths{home: home, data: cfg.DataDir, root: root}, err
}

func resolveNamedPaths(ctx context.Context, cfg Config) (connectionPaths, error) {
	if err := ctx.Err(); err != nil {
		return connectionPaths{}, err
	}
	c := *cfg.Connection
	id, err := uuid.Parse(strings.TrimPrefix(c.ID, "cnx_"))
	if err != nil || c.ID != "cnx_"+id.String() || !filepath.IsAbs(c.Home) || !filepath.IsAbs(cfg.DataDir) {
		return connectionPaths{}, errors.New("named Codex connection requires a stable connection ID and explicit absolute home and state paths")
	}
	home, err := local.CanonicalProviderRoot(c.Home)
	if err != nil {
		return connectionPaths{}, fmt.Errorf("named Codex home unavailable: %w", err)
	}
	// The owner state directory is trusted. Below it, refuse symlink aliases
	// so two account roots cannot accidentally become one recovery key.
	base, err := filepath.EvalSymlinks(cfg.DataDir)
	if err != nil {
		return connectionPaths{}, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return connectionPaths{}, err
	}
	defer func() { _ = root.Close() }()
	parts := []string{"platform-connections", c.ID, "runtime", "codex-supervisor"}
	for _, part := range parts {
		next, err := privateRuntimeDirectory(root, part)
		if err != nil {
			return connectionPaths{}, err
		}
		_ = root.Close()
		root = next
	}
	data := filepath.Join(append([]string{base}, parts[:3]...)...)
	return connectionPaths{home: home, data: data, root: filepath.Join(data, "codex-supervisor"), named: true}, nil
}

func privateRuntimeDirectory(root *os.Root, name string) (*os.Root, error) {
	if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return nil, errors.New("named Codex runtime directory must be private and not a symlink")
	}
	next, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := next.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = next.Close()
		return nil, errors.New("named Codex runtime directory changed during preparation")
	}
	return next, nil
}

func (p connectionPaths) runtimeIdentity(base string) string {
	if p.named {
		return base + "\x00connection-account/v1"
	}
	return base
}
