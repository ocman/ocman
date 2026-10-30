package acpexec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

// ElicitationRequest supplements the SDK's form type with the session/tool
// routing fields used by current ACP agents. The SDK's generated form union
// omits those fields; dropping them would misroute concurrent session questions.
type ElicitationRequest struct {
	SessionID  acp.SessionId
	ToolCallID acp.ToolCallId
	Form       *acp.UnstableCreateElicitationForm
}

type wire struct {
	conn      *acp.Connection
	callbacks callbacks
}

func newWire(cb callbacks, input io.Writer, output io.Reader) *wire {
	w := &wire{callbacks: cb}
	w.conn = acp.NewConnection(w.handle, input, output)
	return w
}

func (w *wire) handle(ctx context.Context, method string, raw json.RawMessage) (any, *acp.RequestError) {
	var result any
	var err error
	switch method {
	case acp.ClientMethodSessionUpdate:
		var p acp.SessionNotification
		if err := decodeACP(raw, &p); err != nil {
			return nil, err
		}
		err = w.callbacks.SessionUpdate(ctx, p)
	case acp.ClientMethodSessionRequestPermission:
		var p acp.RequestPermissionRequest
		if err := decodeACP(raw, &p); err != nil {
			return nil, err
		}
		result, err = w.callbacks.RequestPermission(ctx, p)
	case acp.ClientMethodElicitationCreate:
		if w.callbacks.handlers.Elicitation == nil {
			return nil, acp.NewMethodNotFound(method)
		}
		var p acp.UnstableCreateElicitationRequest
		if err := decodeACP(raw, &p); err != nil {
			return nil, err
		}
		var scope struct {
			SessionID  acp.SessionId  `json:"sessionId"`
			ToolCallID acp.ToolCallId `json:"toolCallId"`
		}
		if err := json.Unmarshal(raw, &scope); err != nil || scope.SessionID == "" || p.Form == nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": "session-scoped form required"})
		}
		result, err = w.callbacks.handlers.Elicitation(ctx, ElicitationRequest{SessionID: scope.SessionID, ToolCallID: scope.ToolCallID, Form: p.Form})
	default:
		return nil, acp.NewMethodNotFound(method)
	}
	if err == nil {
		return result, nil
	}
	var rpc *acp.RequestError
	if errors.As(err, &rpc) {
		return nil, rpc
	}
	return nil, acp.NewInternalError(map[string]any{"error": err.Error()})
}

func decodeACP(raw json.RawMessage, value interface{ Validate() error }) *acp.RequestError {
	if err := json.Unmarshal(raw, value); err != nil {
		return acp.NewInvalidParams(map[string]any{"error": err.Error()})
	}
	if err := value.Validate(); err != nil {
		return acp.NewInvalidParams(map[string]any{"error": err.Error()})
	}
	return nil
}

func (w *wire) Done() <-chan struct{}         { return w.conn.Done() }
func (w *wire) SetLogger(logger *slog.Logger) { w.conn.SetLogger(logger) }

func (w *wire) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.SendRequest[acp.InitializeResponse](w.conn, ctx, acp.AgentMethodInitialize, p)
}

func (w *wire) Authenticate(ctx context.Context, p acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.SendRequest[acp.AuthenticateResponse](w.conn, ctx, acp.AgentMethodAuthenticate, p)
}

func (w *wire) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return acp.SendRequest[acp.NewSessionResponse](w.conn, ctx, acp.AgentMethodSessionNew, p)
}

func (w *wire) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	return acp.SendRequest[acp.LoadSessionResponse](w.conn, ctx, acp.AgentMethodSessionLoad, p)
}

func (w *wire) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.SendRequest[acp.ResumeSessionResponse](w.conn, ctx, acp.AgentMethodSessionResume, p)
}

func (w *wire) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	result, err := acp.SendRequest[acp.PromptResponse](w.conn, ctx, acp.AgentMethodSessionPrompt, p)
	if err != nil && ctx.Err() != nil {
		_ = w.Cancel(context.Background(), acp.CancelNotification{SessionId: p.SessionId})
	}
	return result, err
}

func (w *wire) Cancel(ctx context.Context, p acp.CancelNotification) error {
	return w.conn.SendNotification(ctx, acp.AgentMethodSessionCancel, p)
}

func (w *wire) SetSessionConfigOption(ctx context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SendRequest[acp.SetSessionConfigOptionResponse](w.conn, ctx, acp.AgentMethodSessionSetConfigOption, p)
}

func (w *wire) UnstableForkSession(ctx context.Context, p acp.UnstableForkSessionRequest) (acp.UnstableForkSessionResponse, error) {
	return acp.SendRequest[acp.UnstableForkSessionResponse](w.conn, ctx, acp.AgentMethodSessionFork, p)
}

func (w *wire) CallExtension(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if !strings.HasPrefix(method, "_") {
		return nil, acp.NewInvalidParams(map[string]any{"error": "extension method must start with _"})
	}
	return acp.SendRequest[json.RawMessage](w.conn, ctx, method, params)
}
