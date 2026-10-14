package app

import (
	"context"
	"net"
	"sync/atomic"
)

// reloadBackend rebuilds the composition on the same address, without replacing
// the process or desktop window. A composition requests reload only after drain.
func reloadBackend(ctx context.Context, listener net.Listener, serve func(context.Context, net.Listener, func()) error) error {
	addr := listener.Addr().String()
	defer func() {
		if listener != nil {
			_ = listener.Close()
		}
	}()
	for {
		child, cancel := context.WithCancel(ctx)
		var requested atomic.Bool
		err := serve(child, listener, func() { requested.Store(true); cancel() })
		cancel()
		_ = listener.Close()
		if err != nil || ctx.Err() != nil || !requested.Load() {
			return err
		}
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			return err
		}
	}
}
