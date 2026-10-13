package server

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// providerPromptSource is a provider adapter whose headless event stream
// drives status, idle edges and the permission pipeline (AD-7a).
type providerPromptSource interface {
	SetEventHandler(func(providerprotocol.Event))
	WatchEvents(context.Context) error
}

// watchProviderPrompts installs the prompt pipeline on every provider
// adapter before its headless stream starts, so no asked prompt is missed.
// The stream is push-only: nothing here polls the provider. The streams
// run on workers, so shutdown waits for them.
func (s *Server) watchProviderPrompts(ctx context.Context, workers *sync.WaitGroup) {
	for _, p := range s.registry.Platforms() {
		src, ok := p.(providerPromptSource)
		if !ok {
			continue
		}
		src.SetEventHandler(s.providerPromptHandler(ctx, p))
		workers.Go(func() { _ = src.WatchEvents(ctx) })
	}
}

func (s *Server) providerPromptHandler(ctx context.Context, p platforms.Platform) func(providerprotocol.Event) {
	return func(event providerprotocol.Event) {
		s.aaSvc().HandleProviderEvent(ctx, p, event)
		if event.Type != "permission.replied" {
			return
		}
		var props struct {
			SessionID string `json:"sessionID"`
			RequestID string `json:"requestID"`
		}
		if json.Unmarshal(event.Properties, &props) == nil {
			s.resolvePermissionInbox(ctx, string(p.ID()), props.SessionID, props.RequestID)
		}
	}
}
