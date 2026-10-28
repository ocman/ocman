package codexprovider

import (
	"context"
	"errors"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func threadLoadError(err error) error {
	if activeWriterError(err) {
		return &platforms.BusyError{
			Message: "Another Codex process owns this thread. Close the session in that process and retry.",
			Cause:   errors.Join(platforms.ErrSessionOwnedElsewhere, err),
		}
	}
	return notFound(err)
}

// ownership never starts an app-server and never infers ownership from history.
func (b *backend) ownership(ctx context.Context, id string) (*providerprotocol.SessionOwnership, error) {
	b.mu.Lock()
	c, gen := b.client, b.gen
	b.mu.Unlock()
	if c == nil {
		return &providerprotocol.SessionOwnership{}, nil
	}
	th, err := locallyLoadedThread(ctx, c, id)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	current := b.client == c && b.gen == gen
	b.mu.Unlock()
	loaded := current && th != nil && th.Status.Type != "notLoaded"
	return &providerprotocol.SessionOwnership{SourceKey: c.Info.CodexHome, Loaded: loaded, Idle: loaded && th.Status.Type == "idle"}, nil
}

func activeWriterError(err error) bool {
	var rpc *codexappserver.RPCError
	return errors.As(err, &rpc) && rpc.Code == -32600 && strings.Contains(rpc.Message, "already has an active writer")
}
