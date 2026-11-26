package mcp

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/platforms"
)

// ErrInvalidSessionCreate marks a create failure caused by the caller's
// input; its message is safe to return to the agent.
var ErrInvalidSessionCreate = errors.New("invalid session request")

// SessionCreateStageError marks an unexpected creation failure with the stage
// that failed. The agent sees the stage and a classified cause, never Err's
// text, which can contain host paths or command output.
type SessionCreateStageError struct {
	Stage string
	Err   error
}

func (e *SessionCreateStageError) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e *SessionCreateStageError) Unwrap() error { return e.Err }

// createFailure says which stage failed, why, and whether retrying can help.
func createFailure(err error) string {
	stage := "setup"
	var staged *SessionCreateStageError
	if errors.As(err, &staged) && staged.Stage != "" {
		stage = staged.Stage
	}
	// An uncertain allocation may already exist, whatever its underlying cause;
	// a fresh create gets a new request key and would duplicate it.
	var pending *platforms.CreationPendingError
	if errors.As(err, &pending) {
		return "session creation outcome is uncertain after " + stage + ": session " + pending.SessionID + " may exist. Do not create another session; check it with get, and ask the user before retrying."
	}
	cause, advice := createFailureDetails(err)
	return "session creation failed during " + stage + ": " + cause + ". " + advice
}

func createFailureDetails(err error) (cause, advice string) {
	switch {
	case errors.Is(err, platforms.ErrRemoteOffline):
		cause, advice = "the target machine is not connected", "Retry after it reconnects; ocman never falls back to another machine."
	case errors.Is(err, platforms.ErrPlatformUnreachable):
		cause, advice = "the target platform has no running, ready instance", "Retrying in a minute may succeed; if it keeps failing, ask the user to check the platform connection in ocman."
	case errors.Is(err, platforms.ErrPlatformUnauthenticated):
		cause, advice = "the target platform rejected ocman's credentials", "Do not retry; ask the user to sign in to the platform again."
	case errors.Is(err, platforms.ErrBusy):
		cause, advice = "the target is busy", "Retry shortly."
	case errors.Is(err, context.DeadlineExceeded):
		cause, advice = "the request timed out", "Check sessions list for a new session before retrying once, so a slow creation is not duplicated."
	case errors.Is(err, context.Canceled):
		cause, advice = "the request was cancelled", "Retry only if the session is still needed."
	case errors.Is(err, platforms.ErrUnsupported):
		cause, advice = "the target platform does not support this request", "Do not retry unchanged; ask the user which platform to use."
	default:
		cause, advice = "unexpected server error", `Retrying unchanged is unlikely to help; report this message to the user. Ocman logged the cause as "MCP session creation failed" with this stage.`
	}
	return cause, advice
}

// CreateSessionRequest starts a new session and sends it one prompt.
// Directory wins; when empty the service resolves the project root of the
// calling session (Platform + SessionID), which also picks the owner.
type CreateSessionRequest struct {
	Prompt, Model, Agent, Title string
	Directory                   string
	Platform, SessionID         string
	Worktree                    *bool
	Caller                      *SessionCaller `json:"-"` // Hidden per-call transport context, never a tool argument.
}

// CreatedSession is the sessions create result.
type CreatedSession struct {
	Platform  string `json:"platform"`
	SessionID string `json:"session_id"`
	Directory string `json:"directory"`
}

type sessionCreator interface {
	CreateSession(context.Context, CreateSessionRequest) (CreatedSession, error)
}

func (t *sessionTools) create(ctx context.Context, req mcplib.CallToolRequest) *mcplib.CallToolResult {
	creator, ok := t.svc.(sessionCreator)
	if !ok {
		return mcplib.NewToolResultError("session creation is unavailable")
	}
	in := CreateSessionRequest{
		Prompt: strings.TrimSpace(req.GetString("prompt", "")), Model: strings.TrimSpace(req.GetString("model", "")),
		Agent: strings.TrimSpace(req.GetString("agent", "")), Title: strings.TrimSpace(req.GetString("title", "")),
		Directory: strings.TrimSpace(req.GetString("directory", "")),
		Platform:  strings.TrimSpace(req.GetString("platform", "")), SessionID: strings.TrimSpace(req.GetString("session_id", "")),
	}
	var err error
	in.Caller, err = sessionCaller(req)
	if err != nil {
		return mcplib.NewToolResultError(err.Error())
	}
	if value, exists := req.GetArguments()["worktree"]; exists {
		worktree, ok := value.(bool)
		if !ok {
			return mcplib.NewToolResultError("worktree must be a boolean")
		}
		in.Worktree = &worktree
	}
	switch {
	case in.Prompt == "":
		return mcplib.NewToolResultError("prompt is required")
	case in.Model != "" && !strings.Contains(strings.Trim(in.Model, "/"), "/"):
		return mcplib.NewToolResultError("model must be provider/model")
	case in.Directory != "" && !filepath.IsAbs(in.Directory):
		return mcplib.NewToolResultError("directory must be absolute")
	case in.SessionID != "" && in.Platform == "" && in.Caller == nil:
		return mcplib.NewToolResultError("session_id requires platform")
	case in.Directory == "" && in.SessionID == "" && in.Caller == nil:
		return mcplib.NewToolResultError("directory or the calling session's platform and session_id is required")
	case in.SessionID == "" && in.Caller == nil && (in.Platform == "" || in.Model == ""):
		return mcplib.NewToolResultError("create requires caller platform and session_id, or explicit platform, model, and directory")
	}
	created, err := creator.CreateSession(ctx, in)
	link := ""
	if created.SessionID != "" {
		link = "/session/" + url.PathEscape(created.SessionID) + "?platform=" + url.QueryEscape(created.Platform)
	}
	switch {
	case err != nil && created.SessionID != "":
		// Don't hide the session: the agent can retry the prompt in the UI or inspect it.
		message := "session " + created.SessionID + " was created on " + created.Platform + " but setup or prompt delivery failed"
		var staged *SessionCreateStageError
		if errors.As(err, &staged) && staged.Stage != "" {
			message += " during " + staged.Stage
		}
		cause, _ := createFailureDetails(err)
		message += ": " + cause + ". Prompt delivery is not confirmed; history may be empty. Do not create another session or automatically resend the prompt; inspect this session and ask the user before retrying."
		if link != "" {
			message += "\n" + link
		}
		return mcplib.NewToolResultError(message)
	case errors.Is(err, ErrInvalidSessionCreate):
		return mcplib.NewToolResultError(err.Error())
	case errors.Is(err, platforms.ErrNotFound):
		return mcplib.NewToolResultError("session not found")
	case err != nil:
		return mcplib.NewToolResultError(createFailure(err))
	}
	return toolResultJSON(struct {
		CreatedSession
		URL string `json:"url,omitempty"`
	}{created, link})
}
