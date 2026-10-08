package ocruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// KindNativeTmux is the Instance.Kind reported by the native runtime.
const KindNativeTmux = "native-tmux"

// NativeRuntime hosts OpenCode as ocman always has: one opencode process
// per project in a tmux session, launched with the caller-allocated
// port and reachable over loopback HTTP.
type NativeRuntime struct {
	// launch/kill are seams so tests exercise Launch/Stop without a real
	// tmux binary. Nil values fall back to the tmux package defaults.
	launch func(context.Context, string, string, map[string]string) (session string, err error)
	kill   func(context.Context, string) error

	// httpClient probes /config; nil uses the package default.
	httpClient *http.Client
	auth       ocapi.Auth
}

// NewNativeRuntime returns a NativeRuntime wired to the real tmux
// launcher.
func NewNativeRuntime() *NativeRuntime {
	return NewNativeRuntimeWithAuth(ocapi.New(""))
}

func NewNativeRuntimeWithAuth(auth ocapi.Auth) *NativeRuntime {
	return &NativeRuntime{
		launch: func(ctx context.Context, directory, command string, env map[string]string) (string, error) {
			// The host already checked for a healthy project server. A tmux
			// session alone may only contain shells or a stale OpenCode window.
			name, _, err := tmux.LaunchOpencodeCmdEnvWith(ctx, tmux.DefaultRunner, directory, command, false, env)
			return name, err
		},
		kill: tmux.DefaultRunner.KillSession,
		auth: auth,
	}
}

// Launch threads spec.Port into `opencode --port N`, seeds
// OPENCODE_PERMISSION, and returns the loopback endpoint + tmux session
// name as the instance ID.
func (r *NativeRuntime) Launch(ctx context.Context, spec LaunchSpec) (*Instance, error) {
	if spec.RepoRoot == "" {
		return nil, fmt.Errorf("ocruntime: LaunchSpec.RepoRoot is required")
	}
	if spec.Port <= 0 {
		return nil, fmt.Errorf("ocruntime: LaunchSpec.Port must be a positive allocated port, got %d", spec.Port)
	}
	host := spec.Host
	if host == "" {
		host = "127.0.0.1"
	}

	env := map[string]string{}
	if spec.PermissionJSON != "" && !spec.V2 { // v2 has no OPENCODE_PERMISSION
		env["OPENCODE_PERMISSION"] = spec.PermissionJSON
	}
	r.auth.AddServerEnv(env)

	command := tmux.OpencodeCommandForPort(spec.Port)
	if spec.V2 {
		command = tmux.OpencodeServeCommandForPort(spec.Port)
		// tmux panes inherit the tmux server's environment, not ocman's:
		// carry an OPENCODE_DB override so ocman reads the database the
		// server it launched writes.
		if db := os.Getenv("OPENCODE_DB"); db != "" {
			env["OPENCODE_DB"] = db
		}
	}
	session, err := r.launch(ctx, spec.RepoRoot, command, env)
	if err != nil {
		return nil, fmt.Errorf("ocruntime: launch native tmux opencode: %w", err)
	}

	return &Instance{
		Endpoint: fmt.Sprintf("http://%s:%d", host, spec.Port),
		Kind:     KindNativeTmux,
		ID:       session,
		RepoRoot: spec.RepoRoot,
	}, nil
}

// Probe returns nil only when GET {Endpoint}/config answers 200 and,
// when the instance records an expected RepoRoot, the instance actually
// serving that endpoint is rooted there.
func (r *NativeRuntime) Probe(ctx context.Context, inst *Instance) error {
	if inst == nil || inst.Endpoint == "" {
		return ErrProbeUnreachable
	}
	client := r.httpClient
	if client == nil {
		client = &http.Client{Timeout: defaultProbeClient.Timeout, Transport: r.auth.Transport(http.DefaultTransport)}
	}
	if err := probeConfig(ctx, client, inst.Endpoint); err != nil {
		return err
	}
	if inst.RepoRoot == "" {
		return nil
	}
	return probeIdentity(ctx, client, inst.Endpoint, inst.RepoRoot)
}

// Stop kills the owned tmux session. An already-missing session also requires
// endpoint evidence before it can be confirmed stopped.
func (r *NativeRuntime) Stop(ctx context.Context, inst *Instance) error {
	if inst == nil {
		return fmt.Errorf("ocruntime: Stop requires an instance with an ID")
	}
	if inst.ID == "" {
		if r.endpointAbsent(ctx, inst) {
			return nil
		}
		return fmt.Errorf("ocruntime: Stop requires an instance with an ID")
	}
	if err := r.kill(ctx, inst.ID); err != nil {
		if missingNativeSession(err) && r.endpointAbsent(ctx, inst) {
			return nil
		}
		return fmt.Errorf("ocruntime: stop native tmux opencode: %w", err)
	}
	if inst.Endpoint == "" {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if r.endpointAbsent(stopCtx, inst) {
			return nil
		}
		select {
		case <-stopCtx.Done():
			return fmt.Errorf("ocruntime: endpoint still available after tmux stop: %w", stopCtx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r *NativeRuntime) endpointAbsent(ctx context.Context, inst *Instance) bool {
	if inst.Endpoint == "" {
		return false
	}
	err := r.Probe(ctx, inst)
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, ErrProbeIdentityMismatch)
}

func missingNativeSession(err error) bool {
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		return false
	}
	message := string(exited.Stderr)
	if message == "" {
		message = err.Error()
	}
	message = strings.ToLower(message)
	return strings.Contains(message, "can't find session:") || strings.Contains(message, "no server running") ||
		(strings.Contains(message, "error connecting to") && strings.Contains(message, "no such file or directory"))
}

// compile-time assurance NativeRuntime satisfies Runtime.
var _ Runtime = (*NativeRuntime)(nil)
