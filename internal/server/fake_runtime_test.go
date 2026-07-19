package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// fakeRuntime is a server-package test double for ocruntime.Runtime that
// never spawns a real tmux/opencode process. Launch returns a healthy
// instance at endpoint; Probe reports healthy so EnsureProjectOpencode
// resolves without waiting. Tests inject it via srv.runtime before the
// host router is built.
type fakeRuntime struct {
	endpoint string
}

func (f fakeRuntime) Launch(_ context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	ep := f.endpoint
	if ep == "" {
		ep = "http://127.0.0.1:5599"
	}
	return &ocruntime.Instance{Endpoint: ep, Kind: ocruntime.KindNativeTmux, ID: "sess-name"}, nil
}

func (f fakeRuntime) Probe(context.Context, *ocruntime.Instance) bool { return true }

func (f fakeRuntime) Stop(context.Context, *ocruntime.Instance) error { return nil }
