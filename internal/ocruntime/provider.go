package ocruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

const KindProviderTmux = "provider-tmux"

var providerName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// BackendRuntime launches the ocman executable, never a CLI agent in a TTY.
// Backend children are owned by the supervisor and communicate through pipes.
type BackendRuntime struct {
	PlatformID string
	Binary     string
	Directory  string
	// Args returns backend-specific flags appended at each launch, read
	// then so values bound after startup (the MCP listener) are current.
	Args func() []string
	// Identity names the launch configuration (e.g. CLI and account config)
	// that Args carries; it is part of the instance id, so a supervisor
	// launched with another configuration is recognised and replaced.
	Identity func() string
	launch   func(context.Context, string, []string) error
	stop     func(context.Context, string) error
	alive    func(context.Context, string) (bool, error)
}

func NewBackendRuntime(platformID, binary, directory string) *BackendRuntime {
	return &BackendRuntime{PlatformID: platformID, Binary: binary, Directory: directory,
		launch: func(ctx context.Context, _ string, args []string) error { return tmux.Run(ctx, args...) },
		stop:   tmux.KillManagedSession,
		alive:  tmux.HasManagedSession,
	}
}

func (r *BackendRuntime) Launch(ctx context.Context, spec LaunchSpec) (*Instance, error) {
	if !providerName.MatchString(r.PlatformID) || !filepath.IsAbs(spec.RepoRoot) || !filepath.IsAbs(r.Binary) || !filepath.IsAbs(r.Directory) {
		return nil, fmt.Errorf("provider runtime requires an id and absolute binary, root and state paths")
	}
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	rootHash := sha256.Sum256([]byte(spec.RepoRoot))
	name := r.PlatformID + "-" + hex.EncodeToString(rootHash[:6]) + "-" + r.identity() + "-" + hex.EncodeToString(nonce[:])
	dir := filepath.Join(r.Directory, name)
	socket := filepath.Join(dir, "s")
	if len(socket) > 103 {
		return nil, fmt.Errorf("provider unix socket path exceeds 103 bytes: %s", socket)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// tmux accepts an executable plus argv; avoid a shell quoting layer entirely.
	// Shares the managed server with OpenCode (tmux.ManagedServer).
	args := tmux.ManagedArgs("new-session", "-d", "-s", name, "-n", "provider", "-c", spec.RepoRoot, "--",
		r.Binary, "provider", r.PlatformID, "--socket", socket, "--spool", filepath.Join(dir, "spool"), "--repo", spec.RepoRoot)
	if r.Args != nil {
		args = append(args, r.Args()...)
	}
	if err := r.launch(ctx, spec.RepoRoot, args); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &Instance{Endpoint: "unix://" + socket, Kind: KindProviderTmux, ID: name, RepoRoot: spec.RepoRoot}, nil
}

func providerClient(inst *Instance) (*http.Client, error) {
	if inst == nil {
		return nil, ErrProbeUnreachable
	}
	u, err := url.Parse(inst.Endpoint)
	if err != nil || u.Scheme != "unix" || u.Host != "" || !filepath.IsAbs(u.Path) {
		return nil, ErrProbeUnreachable
	}
	return &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", u.Path)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func providerCall(ctx context.Context, inst *Instance, method, path string, result any) error {
	client, err := providerClient(inst)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, method, "http://provider"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProbeUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: provider HTTP %d", ErrProbeUnreachable, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
		return fmt.Errorf("%w: %w", ErrProbeUnreachable, err)
	}
	return nil
}

func (r *BackendRuntime) Probe(ctx context.Context, inst *Instance) error {
	var hello struct {
		providerprotocol.Hello
		Version *int `json:"protocolVersion"`
	}
	if err := providerCall(ctx, inst, "GET", "/hello", &hello); err != nil {
		return err
	}
	if hello.Version == nil || hello.PlatformID != r.PlatformID || hello.CLIVersion == "" {
		return ErrProbeUnreachable
	}
	return providerprotocol.CheckVersion(*hello.Version)
}

func (r *BackendRuntime) Stop(ctx context.Context, inst *Instance) error {
	if inst == nil || !providerName.MatchString(inst.ID) || inst.Kind != KindProviderTmux {
		return fmt.Errorf("invalid provider instance")
	}
	return r.stop(ctx, inst.ID)
}

// Drain is version-independent: it blocks admission before reporting idle.
func (r *BackendRuntime) Drain(ctx context.Context, inst *Instance) (bool, error) {
	return r.drain(ctx, inst, "/drain")
}

// DrainIdle blocks new turns only if the supervisor is already idle.
// Older supervisors reject this route rather than being drained while busy.
func (r *BackendRuntime) DrainIdle(ctx context.Context, inst *Instance) (bool, error) {
	return r.drain(ctx, inst, "/drain-idle")
}

func (r *BackendRuntime) Resume(ctx context.Context, inst *Instance) error {
	var result providerprotocol.DrainState
	return providerCall(ctx, inst, "POST", "/resume", &result)
}

func (r *BackendRuntime) drain(ctx context.Context, inst *Instance, path string) (bool, error) {
	var drain struct {
		Idle *bool `json:"idle"`
	}
	if err := providerCall(ctx, inst, "POST", path, &drain); err != nil {
		return false, err
	}
	if drain.Idle == nil {
		return false, fmt.Errorf("provider drain omitted idle state")
	}
	return *drain.Idle, nil
}

var _ Runtime = (*BackendRuntime)(nil)

// Alive distinguishes a dead tracked process from an uncertain API timeout.
func (r *BackendRuntime) Alive(ctx context.Context, inst *Instance) (bool, error) {
	if inst == nil || inst.Kind != KindProviderTmux || !providerName.MatchString(inst.ID) {
		return false, fmt.Errorf("invalid provider instance")
	}
	return r.alive(ctx, inst.ID)
}

// identity is a short hash of Identity(), "0" without one.
func (r *BackendRuntime) identity() string {
	if r.Identity == nil {
		return "0"
	}
	sum := sha256.Sum256([]byte(r.Identity()))
	return hex.EncodeToString(sum[:3])
}

// Current reports whether inst was launched with the current launch
// configuration. One that was not keeps running until it is drained.
func (r *BackendRuntime) Current(inst *Instance) bool {
	return r.Identity == nil || (inst != nil && strings.Contains(inst.ID, "-"+r.identity()+"-"))
}
