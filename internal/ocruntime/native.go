package ocruntime

import (
	"context"
	"fmt"
	"net/http"

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
	launch func(directory, command string, env map[string]string) (session string, err error)
	kill   func(session string) error

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
		launch: func(directory, command string, env map[string]string) (string, error) {
			// The host already checked for a healthy project server. A tmux
			// session alone may only contain shells or a stale OpenCode window.
			name, _, err := tmux.LaunchOpencodeCmdEnvWith(tmux.DefaultRunner, directory, command, false, env)
			return name, err
		},
		kill: tmux.DefaultRunner.KillSession,
		auth: auth,
	}
}

// Launch threads spec.Port into `opencode --port N`, seeds
// OPENCODE_PERMISSION, and returns the loopback endpoint + tmux session
// name as the instance ID.
func (r *NativeRuntime) Launch(_ context.Context, spec LaunchSpec) (*Instance, error) {
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
	if spec.PermissionJSON != "" {
		env["OPENCODE_PERMISSION"] = spec.PermissionJSON
	}
	r.auth.AddServerEnv(env)

	command := tmux.OpencodeCommandForPort(spec.Port)
	session, err := r.launch(spec.RepoRoot, command, env)
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

// Stop kills the tmux session backing the instance.
func (r *NativeRuntime) Stop(_ context.Context, inst *Instance) error {
	if inst == nil || inst.ID == "" {
		return fmt.Errorf("ocruntime: Stop requires an instance with an ID")
	}
	if err := r.kill(inst.ID); err != nil {
		return fmt.Errorf("ocruntime: stop native tmux opencode: %w", err)
	}
	return nil
}

// compile-time assurance NativeRuntime satisfies Runtime.
var _ Runtime = (*NativeRuntime)(nil)
