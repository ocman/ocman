package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ToolSessionCapability adds bounded, plugin-owned tools to private sessions.
var ToolSessionCapability = Capability{Name: "session", Version: Version{Major: 1, Minor: 7}}

const SessionToolsGrant = "session.tools"
const SessionToolMethod = "tool"
const SessionStopMethod = "stop"
const SessionToolMaxBytes = 64 << 10

func sessionRequestMethod(c Call) bool {
	if c.Capability != "session" || c.Version.Major != 1 {
		return false
	}
	return c.Method == SessionRunMethod || c.Method == SessionSendMethod && c.Version.Minor >= 5 || c.Method == SessionStopMethod && c.Version.Minor >= 7
}

type SessionTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Context is an opaque plugin-owned routing identity, never a directory.
// Bounds apply to the whole session, not to each tool individually.
type SessionTools struct {
	Context        string        `json:"context"`
	Tools          []SessionTool `json:"tools"`
	MaxCalls       int           `json:"maxCalls"`
	TimeoutSeconds int           `json:"timeoutSeconds"`
	MaxCostUSD     float64       `json:"maxCostUsd,omitempty"`
}

func (p SessionTools) Validate() error {
	if p.MaxCostUSD < 0 || math.IsNaN(p.MaxCostUSD) || math.IsInf(p.MaxCostUSD, 0) {
		return ErrInvalidMessage
	}
	if !actionID.MatchString(p.Context) || len(p.Context) > 128 || len(p.Tools) == 0 || len(p.Tools) > 16 || p.MaxCalls < 1 || p.MaxCalls > 1000 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 3600 {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, tool := range p.Tools {
		var schema map[string]any
		if !actionID.MatchString(tool.Name) || seen[tool.Name] || tool.Description == "" || len(tool.Description) > 2048 || !utf8.ValidString(tool.Description) || strings.TrimSpace(tool.Description) != tool.Description || strings.ContainsFunc(tool.Description, unicode.IsControl) || len(tool.InputSchema) > SessionToolMaxBytes || json.Unmarshal(tool.InputSchema, &schema) != nil || schema["type"] != "object" {
			return ErrInvalidMessage
		}
		seen[tool.Name] = true
	}
	if raw, err := json.Marshal(p); err != nil || len(raw) > SessionToolMaxBytes {
		return ErrInvalidMessage
	}
	return nil
}

type SessionToolCall struct {
	SessionID string          `json:"sessionId"`
	Context   string          `json:"context"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	// CostUSD is cumulative observed session spending before this dispatch.
	// Nil means unknown, never zero. Final completion still carries full usage.
	CostUSD *float64 `json:"costUsd,omitempty"`
}

func (p SessionToolCall) Validate() error {
	if p.CostUSD != nil && (*p.CostUSD < 0 || math.IsNaN(*p.CostUSD) || math.IsInf(*p.CostUSD, 0)) {
		return ErrInvalidMessage
	}
	var args map[string]any
	if !validText(p.SessionID) || !actionID.MatchString(p.Context) || !actionID.MatchString(p.Tool) || len(p.Arguments) > SessionToolMaxBytes || json.Unmarshal(p.Arguments, &args) != nil || args == nil {
		return ErrInvalidMessage
	}
	return nil
}

func SessionToolsAllowed(d Description, grants []string) error {
	if SessionAllowed(d, grants) != nil || !slices.Contains(grants, SessionToolsGrant) || !slices.Contains(d.RequestedGrants, SessionToolsGrant) || !slices.ContainsFunc(d.Capabilities, func(c Capability) bool {
		return c.Name == "session" && c.Version.Major == 1 && c.Version.Minor >= 7
	}) {
		return &WireError{Category: ErrorPermissionDenied}
	}
	return nil
}

// Tool authorizes each callback against current enablement and grants. Calls
// are never automatically retried: plugins own domain validation and receipts.
func (b *SessionBroker) Tool(ctx context.Context, pluginID, operationID string, params SessionToolCall) (json.RawMessage, error) {
	if params.Validate() != nil || !validText(operationID) {
		return nil, &WireError{Category: ErrorInvalidArgument}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	result, err := unaryCall(ctx, b.authorize, b.call, pluginID, SessionToolsAllowed, params, func(Description) Call {
		return Call{OperationID: operationID, Capability: "session", Version: ToolSessionCapability.Version, Method: SessionToolMethod}
	})
	if err == nil && (len(result) > SessionToolMaxBytes || !json.Valid(result)) {
		return nil, ErrInvalidMessage
	}
	return result, err
}

type SessionStopParams struct {
	Context string `json:"context"`
}

func (p SessionStopParams) Validate() error {
	if !actionID.MatchString(p.Context) || len(p.Context) > 128 {
		return ErrInvalidMessage
	}
	return nil
}

func (b *SessionBroker) stopTools(ctx context.Context, pluginID string, call Call) (SessionRunResult, error) {
	var params SessionStopParams
	d := json.NewDecoder(bytes.NewReader(call.Params))
	d.DisallowUnknownFields()
	if call.Capability != "session" || call.Version != ToolSessionCapability.Version || !validText(call.OperationID) || len(call.Params) > SessionToolMaxBytes || d.Decode(&params) != nil || d.Decode(new(any)) != io.EOF || params.Validate() != nil {
		return SessionRunResult{}, &WireError{Category: ErrorInvalidArgument}
	}
	if err := b.authorize(ctx, pluginID, func(d Description, grants []string, _ Config) error { return SessionToolsAllowed(d, grants) }); err != nil {
		return SessionRunResult{}, err
	}
	if b.StopTools == nil {
		return SessionRunResult{}, ErrUnavailable
	}
	return SessionRunResult{}, b.StopTools(ctx, pluginID, params.Context)
}
