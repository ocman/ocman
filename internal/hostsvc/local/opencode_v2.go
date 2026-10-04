package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// OpenCode v2 serves every project from one server: each request names
// its directory. ocman therefore runs a single `opencode serve` per
// machine for v2 instead of one instance per project. It is tracked
// under a dedicated working directory used as its "repo root": a
// directory of its own keeps the tmux launcher from joining (and Stop
// from killing) a session the user opened in, say, their home directory.

// machineRoot is the key and working directory of the v2 machine server.
// It is bound to the database the server writes (OPENCODE_DB, which
// ocman sets from -db): a persisted server for another database is never
// reused, so reads and writes always hit the same file.
//
// Servers for other databases, and v1 per-project instances left from
// before an upgrade, are reaped by RunMachineSupervisor.
func machineRoot() string {
	base, err := os.UserHomeDir()
	if err != nil {
		base = os.TempDir()
	}
	name := "opencode-v2"
	if dbPath := os.Getenv("OPENCODE_DB"); dbPath != "" {
		if abs, err := filepath.Abs(dbPath); err == nil {
			dbPath = abs
		}
		sum := sha256.Sum256([]byte(filepath.Clean(dbPath)))
		name += "-" + hex.EncodeToString(sum[:4])
	}
	return filepath.Join(base, ".local", "share", "ocman", name)
}

// ensureMachine ensures the machine's v2 server and reports it for
// projectDir. RepoRoot stays the project's root so callers that create
// sessions or name worktrees keep working per project.
func (h *Host) ensureMachine(ctx context.Context, projectDir string,
	fn func(context.Context, string) (*hostsvc.EnsureProjectOpencodeResult, error)) (*hostsvc.EnsureProjectOpencodeResult, error) {
	root := machineRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("creating OpenCode v2 working directory: %w", err)
	}
	res, err := h.sfDoDetached(ctx, root, fn)
	if err != nil {
		// Don't keep routing every directory to a server that is gone.
		h.publishMachineServer("")
		return nil, err
	}
	h.publishMachineServer(res.Endpoint)
	out := *res
	out.RepoRoot = projectDir
	if projectDir != "" {
		if root, err := projectOpencodeRoot(ctx, projectDir); err == nil {
			out.RepoRoot = root
		} else if !errors.Is(err, git.ErrNotARepo) {
			log.WithError(err).WithField("dir", projectDir).Debug("host: resolving project root for v2 session")
		}
	}
	return &out, nil
}

// publishMachineServer tells port discovery which server answers every
// directory on this machine.
func (h *Host) publishMachineServer(endpoint string) {
	if h.deps.SetMachineServer == nil {
		return
	}
	port := ""
	if u, err := url.Parse(endpoint); err == nil {
		port = u.Port()
	}
	h.deps.SetMachineServer(port)
}

// machineSupervisorInterval bounds how long a crashed v2 server stays
// down when nothing else asks for it.
const machineSupervisorInterval = 30 * time.Second

// RunMachineSupervisor keeps the v2 machine server online: it ensures
// the server at startup and then periodically, relaunching it when the
// probe fails. A no-op loop while the installed OpenCode is v1.
func (h *Host) RunMachineSupervisor(ctx context.Context) {
	if ocv2.InstalledV2() {
		h.reapNonMachineInstances(ctx)
	}
	tick := time.NewTicker(machineSupervisorInterval)
	defer tick.Stop()
	for {
		if ocv2.InstalledV2() {
			if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: machineRoot()}); err != nil && ctx.Err() == nil {
				log.WithError(err).Warn("host: OpenCode v2 server unavailable")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// reapNonMachineInstances stops and forgets every managed instance other
// than this machine's v2 server: v1 per-project instances left from before
// an upgrade, and v2 servers for another database. Nothing routes to them
// any more, and keeping their rows would make restarts target the shared
// server once per stale row.
func (h *Host) reapNonMachineInstances(ctx context.Context) {
	if h.store == nil {
		return
	}
	rows, err := h.store.List(ctx)
	if err != nil {
		log.WithError(err).Warn("host: listing managed opencode instances")
		return
	}
	keep := machineRoot()
	for root, mi := range rows {
		if root == keep {
			continue
		}
		_, _, _ = h.sf.Do(root, func() (any, error) {
			inst := &ocruntime.Instance{Endpoint: mi.Endpoint, Kind: mi.Kind, ID: mi.RuntimeID, PID: mi.PID, RepoRoot: root}
			if err := h.runtime.Stop(ctx, inst); err != nil {
				log.WithError(err).WithField("repoRoot", root).Debug("host: stopping stale managed opencode")
			}
			h.clearInstance(root)
			if err := h.store.Delete(ctx, root); err != nil {
				log.WithError(err).WithField("repoRoot", root).Warn("host: deleting stale managed opencode row")
			}
			return nil, nil
		})
	}
}
