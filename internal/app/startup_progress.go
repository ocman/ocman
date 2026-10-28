package app

import (
	"context"
	"net"

	"github.com/ocman/ocman/internal/startup"
	"github.com/ocman/ocman/internal/webui"
)

func startBackend(ctx context.Context, ln net.Listener, serve func(context.Context, net.Listener, func()) error) error {
	return reloadBackend(ctx, ln, func(ctx context.Context, ln net.Listener, reload func()) error {
		return startup.Run(ctx, ln, webui.MustFS(), func(ctx context.Context) error {
			return serve(ctx, ln, reload)
		})
	})
}
