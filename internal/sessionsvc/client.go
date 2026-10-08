package sessionsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Client binds the service to a fixed platform id.
type Client struct {
	svc        *Service
	platformID string
}

// Client returns a client bound to platformID.
func (s *Service) Client(platformID string) *Client {
	return &Client{svc: s, platformID: platformID}
}

// CreateSession creates a session on the bound platform.
func (c *Client) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	return c.svc.Create(ctx, c.platformID, req)
}

// SendMessage sends a message via the bound platform.
func (c *Client) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	return c.svc.SendMessage(ctx, c.platformID, req)
}

// SetPermissionRules replaces the bound session's permission rules.
func (c *Client) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	return c.svc.SetPermissionRules(ctx, c.platformID, req)
}

// PermissionRules reads the bound session's current permission rules.
func (c *Client) PermissionRules(ctx context.Context, sessionID string) ([]platforms.PermissionRule, error) {
	p, err := c.svc.resolve(ctx, sessionID, c.platformID)
	if err != nil {
		return nil, err
	}
	return p.PermissionRules(ctx, sessionID)
}
