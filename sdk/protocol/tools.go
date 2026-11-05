package protocol

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ToolCapability is tool.v1: plugin-declared tools that agents discover and
// call through the host's MCP endpoint.
var ToolCapability = Capability{Name: "tool", Version: Version{Major: 1}}

const (
	// ToolAgentGrant approves listing and calling the plugin's declared tools
	// from agents connected to this machine's MCP endpoint.
	ToolAgentGrant   = "tool.agent"
	ToolInvokeMethod = "invoke"

	ToolPrincipalAgent = "agent"
	ToolTransportMCP   = "mcp"

	MaxTools                       = 32
	MaxToolDescriptionBytes        = 4096
	MaxToolSchemaBytes             = 32 << 10
	MaxToolDeclarationBytes        = 256 << 10
	MaxToolArgumentBytes           = 64 << 10
	MaxToolResultBytes             = 256 << 10
	MaxToolTextBytes               = 64 << 10
	MaxToolStaticConfirmationBytes = 512
	MaxToolConfirmationTextBytes   = 1024
	DefaultToolTimeoutSeconds      = 30
	MaxToolTimeoutSeconds          = 300

	// ToolIntentArgument and ToolConfirmationArgument are host-reserved root
	// properties. The host advertises and strips them; plugins never see them.
	ToolIntentArgument       = "intent"
	ToolConfirmationArgument = "confirmation_id"
)

var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

// reservedToolNames are the host's built-in MCP tools when tool 1.0 shipped.
// Later built-ins win collisions at discovery instead of failing validation.
var reservedToolNames = []string{"artifacts", "embed_file", "factory", "factory_control", "inbox", "models", "plugin_session_tool", "pull_requests", "render_ui", "routines", "sessions", "webhooks"}

// ToolDescriptor is one statically declared tool. The approval fingerprint
// covers it, so a changed declaration needs approval again.
type ToolDescriptor struct {
	Name           string           `json:"name"`
	Title          string           `json:"title,omitempty"`
	Description    string           `json:"description"`
	InputSchema    json.RawMessage  `json:"inputSchema"`
	RequiredGrants []string         `json:"requiredGrants,omitempty"`
	Confirmation   string           `json:"confirmation,omitempty"`
	TimeoutSeconds int              `json:"timeoutSeconds,omitempty"`
	Annotations    *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations are MCP hints for clients and models, never authorization.
type ToolAnnotations struct {
	ReadOnly    *bool `json:"readOnly,omitempty"`
	Destructive *bool `json:"destructive,omitempty"`
	Idempotent  *bool `json:"idempotent,omitempty"`
	OpenWorld   *bool `json:"openWorld,omitempty"`
}

// ToolInvocation is the params of a tool.v1 invoke call.
type ToolInvocation struct {
	Tool         string          `json:"tool"`
	Arguments    json.RawMessage `json:"arguments"`
	Context      ToolContext     `json:"context"`
	Confirmation *ToolConfirmed  `json:"confirmation,omitempty"`
}

// ToolContext carries only host-established facts. The MCP connection
// authenticates the installation, so no session or owner identity is sent.
type ToolContext struct {
	Principal string   `json:"principal"`
	Transport string   `json:"transport"`
	Grants    []string `json:"grants"`
}

// ToolConfirmed attests that the user approved this call. Key is empty for a
// tool's static confirmation, else the plugin's requested key.
type ToolConfirmed struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Text string `json:"text"`
}

// ToolResult is the terminal value of invoke: text and/or a data object,
// optionally a domain error, or exclusively a confirmation request.
type ToolResult struct {
	Text         string                   `json:"text,omitempty"`
	Data         json.RawMessage          `json:"data,omitempty"`
	IsError      bool                     `json:"isError,omitempty"`
	Confirmation *ToolConfirmationRequest `json:"confirmation,omitempty"`
}

// ToolConfirmationRequest asks the host to obtain the user's approval. The
// plugin must not have made any change before returning it.
type ToolConfirmationRequest struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Timeout is the declared or default per-call deadline.
func (t ToolDescriptor) Timeout() time.Duration {
	if t.TimeoutSeconds == 0 {
		return DefaultToolTimeoutSeconds * time.Second
	}
	return time.Duration(t.TimeoutSeconds) * time.Second
}

// Granted reports whether current approvals allow listing and calling t.
func (t ToolDescriptor) Granted(grants []string) bool {
	if !slices.Contains(grants, ToolAgentGrant) {
		return false
	}
	for _, g := range t.RequiredGrants {
		if !slices.Contains(grants, g) {
			return false
		}
	}
	return true
}

// ToolsAllowed reports whether d negotiates tool major 1 and holds tool.agent.
func (d Description) ToolsAllowed(grants []string) bool {
	return d.Scope == ScopeOwner && len(d.Tools) > 0 && slices.Contains(grants, ToolAgentGrant) &&
		slices.ContainsFunc(d.Capabilities, func(c Capability) bool {
			return c.Name == ToolCapability.Name && c.Version.Major == ToolCapability.Version.Major
		})
}

// Tool finds a declared tool by name.
func (d Description) Tool(name string) (ToolDescriptor, bool) {
	i := slices.IndexFunc(d.Tools, func(t ToolDescriptor) bool { return t.Name == name })
	if i < 0 {
		return ToolDescriptor{}, false
	}
	return d.Tools[i], true
}

// ReservedToolName reports names a plugin tool can never declare.
func ReservedToolName(name string) bool {
	return strings.HasPrefix(name, "ocman") || strings.HasPrefix(name, "plugin_") || slices.Contains(reservedToolNames, name)
}

// toolText accepts trimmed UTF-8 without control characters other than
// newlines and tabs.
func toolText(s string, limit int) bool {
	return s != "" && len(s) <= limit && utf8.ValidString(s) && strings.TrimSpace(s) == s &&
		!strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

func (d Description) validateTools() error {
	declared := slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == ToolCapability.Name })
	if !declared {
		if len(d.Tools) > 0 {
			return ErrInvalidMessage
		}
		return nil
	}
	if d.Scope != ScopeOwner || !slices.Contains(d.RequestedGrants, ToolAgentGrant) || len(d.Tools) == 0 || len(d.Tools) > MaxTools {
		return ErrInvalidMessage
	}
	if raw, err := json.Marshal(d.Tools); err != nil || len(raw) > MaxToolDeclarationBytes {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, t := range d.Tools {
		if err := t.validate(d.RequestedGrants); err != nil || seen[t.Name] {
			return ErrInvalidMessage
		}
		seen[t.Name] = true
	}
	return nil
}

func (t ToolDescriptor) validate(requested []string) error {
	if !toolName.MatchString(t.Name) || ReservedToolName(t.Name) || (t.Title != "" && !validText(t.Title)) ||
		!toolText(t.Description, MaxToolDescriptionBytes) || (t.Confirmation != "" && !toolText(t.Confirmation, MaxToolStaticConfirmationBytes)) ||
		t.TimeoutSeconds < 0 || t.TimeoutSeconds > MaxToolTimeoutSeconds || len(t.InputSchema) > MaxToolSchemaBytes {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, g := range t.RequiredGrants {
		if !slices.Contains(requested, g) || seen[g] {
			return ErrInvalidMessage
		}
		seen[g] = true
	}
	schema, err := compileToolSchema(t.InputSchema)
	if err != nil || schema.Type != "object" {
		return ErrInvalidMessage
	}
	if _, ok := schema.Properties[ToolIntentArgument]; ok {
		return ErrInvalidMessage
	}
	if _, ok := schema.Properties[ToolConfirmationArgument]; ok {
		return ErrInvalidMessage
	}
	return nil
}

// ValidateToolSchema checks that raw is an object schema in the tool.v1 subset.
func ValidateToolSchema(raw json.RawMessage) error {
	if len(raw) > MaxToolSchemaBytes {
		return ErrInvalidMessage
	}
	schema, err := compileToolSchema(raw)
	if err != nil || schema.Type != "object" {
		return ErrInvalidMessage
	}
	return nil
}

// DecodeToolInvocation strictly decodes invoke params.
func DecodeToolInvocation(raw []byte) (ToolInvocation, error) {
	var inv ToolInvocation
	if decodePaneJSON(raw, &inv) != nil || inv.Validate() != nil {
		return ToolInvocation{}, ErrInvalidMessage
	}
	return inv, nil
}

func (inv ToolInvocation) Validate() error {
	var args map[string]any
	if !toolName.MatchString(inv.Tool) || len(inv.Arguments) > MaxToolArgumentBytes || json.Unmarshal(inv.Arguments, &args) != nil || args == nil ||
		inv.Context.Principal != ToolPrincipalAgent || inv.Context.Transport != ToolTransportMCP || inv.Context.Grants == nil {
		return ErrInvalidMessage
	}
	for _, g := range inv.Context.Grants {
		if !settingKey.MatchString(g) {
			return ErrInvalidMessage
		}
	}
	if c := inv.Confirmation; c != nil && (!actionID.MatchString(c.ID) || (c.Key != "" && !actionID.MatchString(c.Key)) || !toolText(c.Text, MaxToolConfirmationTextBytes)) {
		return ErrInvalidMessage
	}
	return nil
}

// DecodeToolResult strictly decodes and validates a plugin's terminal value.
func DecodeToolResult(raw []byte) (ToolResult, error) {
	var r ToolResult
	if len(raw) > MaxToolResultBytes || decodePaneJSON(raw, &r) != nil || r.Validate() != nil {
		return ToolResult{}, ErrInvalidMessage
	}
	return r, nil
}

func (r ToolResult) Validate() error {
	if r.Confirmation != nil {
		c := r.Confirmation
		if r.Text != "" || len(r.Data) > 0 || r.IsError || !actionID.MatchString(c.Key) || !toolText(c.Text, MaxToolConfirmationTextBytes) {
			return ErrInvalidMessage
		}
		return nil
	}
	if r.Text == "" && len(r.Data) == 0 || len(r.Text) > MaxToolTextBytes || !utf8.ValidString(r.Text) {
		return ErrInvalidMessage
	}
	if len(r.Data) > 0 {
		var object map[string]any
		if json.Unmarshal(r.Data, &object) != nil || object == nil {
			return ErrInvalidMessage
		}
	}
	if raw, err := json.Marshal(r); err != nil || len(raw) > MaxToolResultBytes {
		return ErrInvalidMessage
	}
	return nil
}
