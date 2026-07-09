// Package sessionsvc owns session-mutation semantics — input
// validation, adapter selection, and side-effect hooks — so the REST
// handlers, the MCP tools, and the remote gRPC server share one code
// path (the same shape loops.Service gives agent loops).
package sessionsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// Registry is the consumer-side subset of *platforms.Registry the
// service needs.
type Registry interface {
	Get(id platforms.ID) (platforms.Platform, bool)
	Platforms() []platforms.Platform
	PlatformForSession(ctx context.Context, sessionID string) (platforms.Platform, bool)
}

// Hooks are optional callbacks fired around mutations. Nil fields are
// skipped. They exist so transport-independent side effects (auto-approve
// judge cancellation, projects-index refresh) fire no matter which
// transport initiated the mutation.
type Hooks struct {
	// PermissionReplied fires after the owning adapter is resolved and
	// before a permission reply is forwarded to it. The server wires
	// this to cancelAutoApprove: the user has decided, so the AI
	// judge's verdict must not race their answer.
	PermissionReplied func(sessionID, permissionID string)
	// SessionCreated fires after a session is successfully created.
	// The server wires this to an async projects-index refresh.
	SessionCreated func()
}

// Service validates and dispatches session mutations to the owning
// platform adapter.
type Service struct {
	registry Registry
	hooks    Hooks
}

// New builds a Service over the given registry.
func New(registry Registry, hooks Hooks) *Service {
	return &Service{registry: registry, hooks: hooks}
}

// ValidationError marks bad caller input. Transports map it to their
// native shape: HTTP 400, gRPC InvalidArgument.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func validation(msg string) error { return &ValidationError{msg: msg} }

// ErrNoPlatformAvailable is returned by Create when no registered
// platform is available. HTTP maps it to 503.
var ErrNoPlatformAvailable = errors.New("no platform available to create a session")

// resolve returns the adapter for a session. A non-empty platformID is
// honoured first (AD-2b: two hosts may share a session_id, so remote
// sessions must be addressed by their compound platform key); otherwise
// the registry's reverse lookup decides ownership.
func (s *Service) resolve(ctx context.Context, sessionID, platformID string) (platforms.Platform, error) {
	if platformID != "" {
		p, ok := s.registry.Get(platforms.ID(platformID))
		if !ok {
			return nil, fmt.Errorf("unknown platform %q: %w", platformID, platforms.ErrNotFound)
		}
		return p, nil
	}
	p, ok := s.registry.PlatformForSession(ctx, sessionID)
	if !ok {
		return nil, fmt.Errorf("no platform owns session %q: %w", sessionID, platforms.ErrNotFound)
	}
	return p, nil
}

// SendMessage sends a prompt to a session.
func (s *Service) SendMessage(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	if req.Message == "" && len(req.Images) == 0 {
		return validation("message or images required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.SendMessage(ctx, req)
}

// ExecuteCommand runs a slash command in a session.
func (s *Service) ExecuteCommand(ctx context.Context, platformID string, req platforms.ExecuteCommandRequest) error {
	if req.Command == "" {
		return validation("command is required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.ExecuteCommand(ctx, req)
}

// RunShell runs a raw shell command in the session's working directory.
func (s *Service) RunShell(ctx context.Context, platformID string, req platforms.RunShellRequest) error {
	if strings.TrimSpace(req.Command) == "" {
		return validation("command is required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.RunShell(ctx, req)
}

// Rename changes a session's title.
func (s *Service) Rename(ctx context.Context, platformID string, req platforms.RenameSessionRequest) error {
	if req.Title == "" {
		return validation("title is required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.RenameSession(ctx, req)
}

// Abort cancels a session's in-flight turn.
func (s *Service) Abort(ctx context.Context, platformID string, req platforms.AbortRequest) error {
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.Abort(ctx, req)
}

// Compact compacts a session's history.
func (s *Service) Compact(ctx context.Context, platformID string, req platforms.CompactRequest) error {
	if req.ProviderID == "" || req.ModelID == "" {
		return validation("providerID and modelID are required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.Compact(ctx, req)
}

// SetPermissionRules replaces a session's permission rules. Rules are
// normalized in place: an empty pattern defaults to "*".
func (s *Service) SetPermissionRules(ctx context.Context, platformID string, req platforms.SetPermissionRulesRequest) error {
	if len(req.Rules) > 100 {
		return validation("too many rules")
	}
	for i := range req.Rules {
		rule := &req.Rules[i]
		if rule.Permission == "" {
			return validation("rule permission is required")
		}
		if rule.Pattern == "" {
			rule.Pattern = "*"
		}
		switch rule.Action {
		case "allow", "deny", "ask":
		default:
			return validation("invalid rule action: expected allow, deny, or ask")
		}
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.SetPermissionRules(ctx, req)
}

// RespondPermission replies to a pending permission prompt.
func (s *Service) RespondPermission(ctx context.Context, platformID string, req platforms.RespondPermissionRequest) error {
	switch req.Reply {
	case "once", "always", "reject":
	default:
		return validation("invalid reply value: expected once, always, or reject")
	}
	if req.PermissionID == "" {
		return validation("permission ID is required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	if s.hooks.PermissionReplied != nil {
		s.hooks.PermissionReplied(req.SessionID, req.PermissionID)
	}
	return p.RespondPermission(ctx, req)
}

// RespondQuestion answers a pending question.
func (s *Service) RespondQuestion(ctx context.Context, platformID string, req platforms.RespondQuestionRequest) error {
	if req.RequestID == "" {
		return validation("question ID is required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.RespondQuestion(ctx, req)
}

// RejectQuestion rejects a pending question.
func (s *Service) RejectQuestion(ctx context.Context, platformID string, req platforms.RejectQuestionRequest) error {
	if req.RequestID == "" {
		return validation("question ID is required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	return p.RejectQuestion(ctx, req)
}

// Create creates a new session. When platformID is empty the adapter is
// chosen automatically iff exactly one platform is available.
func (s *Service) Create(ctx context.Context, platformID string, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if req.Directory == "" {
		return nil, validation("directory is required")
	}
	var adapter platforms.Platform
	if platformID != "" {
		p, ok := s.registry.Get(platforms.ID(platformID))
		if !ok {
			return nil, validation("unknown platform")
		}
		adapter = p
	} else {
		for _, p := range s.registry.Platforms() {
			if !p.Available(ctx) {
				continue
			}
			if adapter != nil {
				return nil, validation("multiple platforms available — specify ?platform=<id>")
			}
			adapter = p
		}
	}
	if adapter == nil {
		return nil, ErrNoPlatformAvailable
	}
	createPhase := srvtiming.Begin(ctx, "create_session")
	resp, err := adapter.CreateSession(ctx, req)
	createPhase.EndWithDesc("adapter.CreateSession")
	if err != nil {
		return nil, err
	}
	if s.hooks.SessionCreated != nil {
		s.hooks.SessionCreated()
	}
	return resp, nil
}

// Client binds the service to a fixed platform id, exposing the narrow
// CreateSession/SendMessage surface the MCP launcher and comm tools use.
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

// SetPermissionRules replaces a session's permission ruleset via the
// bound platform.
func (c *Client) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	return c.svc.SetPermissionRules(ctx, c.platformID, req)
}
