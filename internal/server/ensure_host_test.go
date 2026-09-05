package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type ensureHost struct {
	hostsvc.Host
	ensure func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error)
}

func (h *ensureHost) RemoteID() string { return "local" }

func (h *ensureHost) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return h.ensure(ctx, req)
}
