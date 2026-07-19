// Package ocruntime abstracts how a project's OpenCode instance is
// hosted. The Runtime interface is the plug point for alternative
// hosting strategies (a container runtime lands in epic #375); the
// native-tmux implementation here reproduces ocman's existing behaviour:
// one opencode process per project in a tmux session, reachable over
// loopback HTTP.
package ocruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
)

// LaunchSpec describes a requested OpenCode instance. The port is
// allocated by the host (AD-3: allocation is host policy) and threaded
// through so the runtime launches on a known endpoint instead of
// letting opencode pick one.
type LaunchSpec struct {
	RepoRoot       string // project checkout the instance is rooted at
	Host           string // host to bind/reach (e.g. "127.0.0.1")
	Port           int    // ocman-allocated port
	PermissionJSON string // seeded as OPENCODE_PERMISSION (empty = none)
}

// Instance is a launched OpenCode instance.
type Instance struct {
	Endpoint string // full base URL, e.g. "http://127.0.0.1:41235"
	Kind     string // runtime kind, e.g. "native-tmux"
	ID       string // runtime-specific handle (tmux session name here)
	PID      int    // process id when known (0 for tmux — the pane owns it)
}

var ErrProbeUnreachable = errors.New("OpenCode instance is unreachable")

// Runtime hosts a project's OpenCode instance.
type Runtime interface {
	// Launch starts an instance for spec and returns its handle. The
	// caller-allocated spec.Port is threaded to the process.
	Launch(ctx context.Context, spec LaunchSpec) (*Instance, error)
	// Probe returns nil when the instance is serving, ErrAuthentication
	// for a credential mismatch, or ErrProbeUnreachable otherwise.
	Probe(ctx context.Context, inst *Instance) error
	// Stop tears the instance down.
	Stop(ctx context.Context, inst *Instance) error
}

// AllocateLoopbackPort returns a free TCP port on the loopback
// interface by binding to :0 and closing immediately. There is an
// inherent TOCTOU window (another process could grab the port before
// the caller binds it) — acceptable here because opencode retries and
// this matches the standard free-port idiom.
//
// ponytail: bind→close is the standard trick; add a retry loop only if
// the race ever bites in practice.
func AllocateLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate loopback port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// probeConfig reports whether GET {endpoint}/config answers 200. It is
// the shared Probe implementation, split out so tests can drive it
// against an httptest server. A non-200, a refused connection, or any
// transport error is classified as unreachable; 401/403 is authentication.
func probeConfig(ctx context.Context, client *http.Client, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/config", nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProbeUnreachable, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ocapi.ErrAuthentication) {
			return err
		}
		return fmt.Errorf("%w: %w", ErrProbeUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: upstream HTTP %d", ErrProbeUnreachable, resp.StatusCode)
	}
	return nil
}

var defaultProbeClient = &http.Client{Timeout: 2 * time.Second}
