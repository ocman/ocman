package acpexec

import (
	"context"
	"fmt"
	"path/filepath"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
)

// Authenticate invokes an advertised agent auth method. Terminal-login methods
// are intentionally not advertised by this client; those need a separate UI flow.
func (c *Client) Authenticate(ctx context.Context, p acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	for _, method := range c.info.AuthMethods {
		if method.Agent != nil && method.Agent.Id == p.MethodId {
			return c.conn.Authenticate(ctx, p)
		}
	}
	return acp.AuthenticateResponse{}, fmt.Errorf("ACP authentication method %q: %w", p.MethodId, platforms.ErrUnsupported)
}

func (c *Client) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if err := absoluteDirectory(p.Cwd); err != nil {
		return acp.NewSessionResponse{}, err
	}
	if p.McpServers == nil {
		p.McpServers = []acp.McpServer{}
	}
	return c.conn.NewSession(ctx, p)
}

// LoadSession replays agent history through Update. This does not replace the
// native history reader or prove ownership of an externally running session.
func (c *Client) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	if !c.info.AgentCapabilities.LoadSession {
		return acp.LoadSessionResponse{}, platforms.ErrUnsupported
	}
	if err := absoluteDirectory(p.Cwd); err != nil {
		return acp.LoadSessionResponse{}, err
	}
	if p.McpServers == nil {
		p.McpServers = []acp.McpServer{}
	}
	return c.conn.LoadSession(ctx, p)
}

// ResumeSession resumes without replay. Never substitute LoadSession implicitly:
// its replay semantics differ and may duplicate live messages.
func (c *Client) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	if c.info.AgentCapabilities.SessionCapabilities.Resume == nil {
		return acp.ResumeSessionResponse{}, platforms.ErrUnsupported
	}
	if err := absoluteDirectory(p.Cwd); err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	return c.conn.ResumeSession(ctx, p)
}

// Prompt returns after the turn's preceding Update callbacks have finished.
// A context cancellation requests cancellation of the remote turn too, but is
// not proof the turn stopped. Callers must not retry or release ownership on it.
func (c *Client) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	result, err := c.conn.Prompt(ctx, p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.updateErr != nil {
		return acp.PromptResponse{}, fmt.Errorf("recording ACP update: %w", c.updateErr)
	}
	return result, err
}

// Cancel requests interruption. The eventual Prompt result confirms settlement.
func (c *Client) Cancel(ctx context.Context, sessionID acp.SessionId) error {
	return c.conn.Cancel(ctx, acp.CancelNotification{SessionId: sessionID})
}

func absoluteDirectory(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("ACP session directory must be absolute")
	}
	return nil
}
