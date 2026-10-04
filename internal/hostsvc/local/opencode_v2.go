package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
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
// ponytail: a server for a database ocman no longer uses keeps running
// until stopped by hand; reap stale machine rows if switching -db becomes
// common.
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
	dir := filepath.Join(base, ".local", "share", "ocman", name)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// ensureMachine ensures the machine's v2 server and reports it for
// projectDir. RepoRoot stays the project's root so callers that create
// sessions or name worktrees keep working per project.
func (h *Host) ensureMachine(ctx context.Context, projectDir string,
	fn func(context.Context, string) (*hostsvc.EnsureProjectOpencodeResult, error)) (*hostsvc.EnsureProjectOpencodeResult, error) {
	res, err := h.sfDoDetached(ctx, machineRoot(), fn)
	if err != nil {
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
