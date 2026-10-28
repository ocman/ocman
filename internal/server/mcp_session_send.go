package server

import (
	"context"
	"strings"

	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
)

// SendSessionMessage uses the composer's direct-send path, never the held queue.
func (s sessionMCPService) SendSessionMessage(ctx context.Context, platform, id, prompt string) (internalmcp.SentSession, error) {
	if strings.HasPrefix(id, "ocm_") || s.server.sessions.CanonicalSessionReferences(platform) {
		info, err := s.server.sessions.LookupSessionReference(ctx, platform, id)
		if err != nil {
			return internalmcp.SentSession{}, err
		}
		platform, id = info.Platform, s.server.sessions.CoreSessionReference(info)
	}
	if platform == "" {
		adapter, _, err := s.findSession(ctx, id, 0)
		if err != nil {
			return internalmcp.SentSession{}, err
		}
		platform = string(adapter.ID())
	} else {
		// Check the explicit recipient without falling back to another owner.
		adapter, ok := s.server.registry.Get(platforms.ID(platform))
		if !ok {
			return internalmcp.SentSession{}, platforms.ErrNotFound
		}
		detail, err := adapter.Session(ctx, id, 0, 0)
		if err != nil {
			return internalmcp.SentSession{}, err
		}
		if detail == nil || detail.Session == nil {
			return internalmcp.SentSession{}, platforms.ErrNotFound
		}
	}
	if err := s.server.sendNow(ctx, platform, platforms.SendMessageRequest{SessionID: id, Message: prompt}); err != nil {
		return internalmcp.SentSession{}, err
	}
	return internalmcp.SentSession{Platform: platform, SessionID: id, Sent: true}, nil
}
