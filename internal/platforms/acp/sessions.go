package acpexec

import (
	"context"
	"encoding/json"
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
// Replayed history that cannot be recorded fails the load.
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
	return sessionCall(ctx, c, p.SessionId, false, func() (acp.LoadSessionResponse, error) {
		return c.conn.LoadSession(ctx, p)
	})
}

// sessionCall runs one operation on sessionID. An update for the session that
// cannot be recorded while it runs fails the operation. A failure recorded
// while no operation ran fails the next one before anything is sent.
func sessionCall[T any](ctx context.Context, c *Client, sessionID acp.SessionId, prompt bool, call func() (T, error)) (T, error) {
	var zero T
	op, err := c.beginSessionOp(ctx, sessionID, prompt)
	if err != nil {
		return zero, err
	}
	result, err := call()
	if recordErr := c.endSessionOp(sessionID, op); recordErr != nil {
		return zero, recordErr
	}
	return result, err
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
	return sessionCall(ctx, c, p.SessionId, false, func() (acp.ResumeSessionResponse, error) {
		return c.conn.ResumeSession(ctx, p)
	})
}

// Prompt returns after the turn's preceding Update callbacks have finished.
// A context cancellation requests cancellation of the remote turn too, but is
// not proof the turn stopped. Callers must not retry or release ownership on it.
func (c *Client) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	result, err := sessionCall(ctx, c, p.SessionId, true, func() (acp.PromptResponse, error) {
		return c.conn.Prompt(ctx, p)
	})
	c.mu.Lock()
	updateErr := c.updateErr
	c.mu.Unlock()
	if updateErr != nil {
		return acp.PromptResponse{}, fmt.Errorf("recording ACP update: %w", updateErr)
	}
	return result, err
}

// Cancel requests interruption. The eventual Prompt result confirms settlement.
func (c *Client) Cancel(ctx context.Context, sessionID acp.SessionId) error {
	return c.conn.Cancel(ctx, acp.CancelNotification{SessionId: sessionID})
}

// SetConfigOption selects an option advertised in the session's config options.
func (c *Client) SetConfigOption(ctx context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	var sessionID acp.SessionId
	switch {
	case p.ValueId != nil:
		sessionID = p.ValueId.SessionId
	case p.Boolean != nil:
		sessionID = p.Boolean.SessionId
	}
	return sessionCall(ctx, c, sessionID, false, func() (acp.SetSessionConfigOptionResponse, error) {
		return c.conn.SetSessionConfigOption(ctx, p)
	})
}

func (c *Client) ForkSession(ctx context.Context, p acp.UnstableForkSessionRequest) (acp.UnstableForkSessionResponse, error) {
	if c.info.AgentCapabilities.SessionCapabilities.Fork == nil {
		return acp.UnstableForkSessionResponse{}, platforms.ErrUnsupported
	}
	if err := absoluteDirectory(p.Cwd); err != nil {
		return acp.UnstableForkSessionResponse{}, err
	}
	return sessionCall(ctx, c, p.SessionId, false, func() (acp.UnstableForkSessionResponse, error) {
		return c.conn.UnstableForkSession(ctx, p)
	})
}

// CallExtension is for explicitly negotiated, agent-specific operations. It
// never substitutes another operation after a failure.
func (c *Client) CallExtension(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.conn.CallExtension(ctx, method, params)
}

func absoluteDirectory(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("ACP session directory must be absolute")
	}
	return nil
}
