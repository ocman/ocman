package server

import (
	"context"
	"strings"

	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
)

// SendSessionMessage uses the composer's direct-send path, never the held queue.
func (s sessionMCPService) SendSessionMessage(ctx context.Context, platform, id, prompt string) (internalmcp.SentSession, error) {
	platform, id, _, err := s.resolveTarget(ctx, platform, id)
	if err != nil {
		return internalmcp.SentSession{}, err
	}
	if err := s.server.sendNow(ctx, platform, platforms.SendMessageRequest{SessionID: id, Message: prompt}); err != nil {
		return internalmcp.SentSession{}, err
	}
	return internalmcp.SentSession{Platform: platform, SessionID: id, Sent: true}, nil
}

// resolveTarget resolves a session an MCP action mutates. An explicit
// platform is checked without falling back to another owner, so a
// disconnected remote fails closed; an omitted one searches every adapter.
func (s sessionMCPService) resolveTarget(ctx context.Context, platform, id string) (string, string, *platforms.SessionDetail, error) {
	if strings.HasPrefix(id, "ocm_") || s.server.sessions.CanonicalSessionReferences(platform) {
		info, err := s.server.sessions.LookupSessionReference(ctx, platform, id)
		if err != nil {
			return "", "", nil, err
		}
		platform, id = info.Platform, s.server.sessions.CoreSessionReference(info)
	}
	if platform == "" {
		adapter, detail, err := s.findSession(ctx, id, 0)
		if err != nil {
			return "", "", nil, err
		}
		return string(adapter.ID()), id, detail, nil
	}
	detail, err := s.readTarget(ctx, platform, id)
	return platform, id, detail, err
}

func (s sessionMCPService) readTarget(ctx context.Context, platform, id string) (*platforms.SessionDetail, error) {
	adapter, ok := s.server.registry.Get(platforms.ID(platform))
	if !ok {
		return nil, platforms.ErrNotFound
	}
	detail, err := adapter.Session(ctx, id, 0, 0)
	if err != nil {
		return nil, err
	}
	if detail == nil || detail.Session == nil {
		return nil, platforms.ErrNotFound
	}
	return detail, nil
}
