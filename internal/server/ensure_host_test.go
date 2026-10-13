package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type ensureHost struct {
	hostsvc.Host
	owner  string
	ensure func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error)
}

func (h *ensureHost) RemoteID() string {
	if h.owner != "" {
		return h.owner
	}
	return "local"
}

func (h *ensureHost) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
	return h.ensure(ctx, req)
}
