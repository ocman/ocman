package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func toolSessionParams() SessionRunParams {
	return SessionRunParams{WorkspaceID: "workspace-a", Prompt: "Inspect inventory", Tools: &SessionTools{Context: "run-a", MaxCalls: 4, TimeoutSeconds: 60,
		Tools: []SessionTool{{Name: "lookup", Description: "Look up an inventory item", InputSchema: json.RawMessage(`{"type":"object"}`)}}}}
}

func TestToolSessionValidation(t *testing.T) {
	valid := toolSessionParams()
	if valid.Validate() != nil {
		t.Fatal("valid tool session rejected")
	}
	valid.Tools.Tools[0].Description = strings.Repeat("Tool documentation. ", 20) + "End."
	if valid.Validate() != nil {
		t.Fatal("bounded tool documentation was treated as a short identifier")
	}
	for _, change := range []func(*SessionRunParams){
		func(p *SessionRunParams) { p.NoTools = true },
		func(p *SessionRunParams) { p.ReadOnly = true },
		func(p *SessionRunParams) { p.Agent = "unrestricted" },
		func(p *SessionRunParams) { p.PermissionProfile = "all" },
		func(p *SessionRunParams) { p.Subpath = "source" },
		func(p *SessionRunParams) { p.WorkspaceID = "" },
		func(p *SessionRunParams) { p.Tools.MaxCalls = 0 },
		func(p *SessionRunParams) { p.Tools.TimeoutSeconds = 3601 },
		func(p *SessionRunParams) { p.Tools.Context = "../escape" },
		func(p *SessionRunParams) { p.Tools.Tools[0].InputSchema = json.RawMessage(`{"type":"string"}`) },
		func(p *SessionRunParams) { p.Tools.Tools = append(p.Tools.Tools, p.Tools.Tools[0]) },
	} {
		p := toolSessionParams()
		change(&p)
		if p.Validate() == nil {
			t.Fatalf("invalid tool session admitted: %+v", p)
		}
	}
}

func TestToolSessionAdmissionRequiresNewVersionAndGrant(t *testing.T) {
	d := sessionDescription()
	d.Capabilities = []Capability{ToolSessionCapability}
	d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant, SessionToolsGrant}
	grants := append([]string{}, d.RequestedGrants...)
	starts := 0
	b := NewSessionBroker(func(_ context.Context, _ string, use func(Description, []string, Config) error) error {
		return use(d, grants, Config{})
	},
		func(context.Context, string, Config, SessionRunParams, string, string) (string, error) {
			starts++
			return "session-a", nil
		}, nil)
	b.ResolveWorkspace = func(context.Context, string, string) (string, error) { return "/private/workspace", nil }
	call := sessionRunEvent(t, toolSessionParams())
	call.Version = AssociatedSessionCapability.Version
	if _, err := b.Run(t.Context(), d.ID, call); err == nil {
		t.Fatal("old version admitted tools")
	}
	call.Version = ToolSessionCapability.Version
	grants = []string{SessionRunGrant, SessionPluginGrant}
	if _, err := b.Run(t.Context(), d.ID, call); err == nil {
		t.Fatal("ungranted tools admitted")
	}
	grants = d.RequestedGrants
	if _, err := b.Run(t.Context(), d.ID, call); err != nil || starts != 1 {
		t.Fatalf("admission: %v, starts=%d", err, starts)
	}
	if d.Validate() != nil {
		t.Fatal("tool declaration rejected")
	}
	d.Capabilities = []Capability{AssociatedSessionCapability}
	if d.Validate() == nil {
		t.Fatal("old declaration accepted tools grant")
	}
}

func TestToolCallbackRechecksGrantsAndBoundsResult(t *testing.T) {
	d := sessionDescription()
	d.Capabilities = []Capability{ToolSessionCapability}
	d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant, SessionToolsGrant}
	grants := d.RequestedGrants
	result := json.RawMessage(`{"items":["fixture"]}`)
	calls := 0
	b := NewSessionBroker(func(_ context.Context, _ string, use func(Description, []string, Config) error) error {
		return use(d, grants, Config{})
	}, nil,
		func(_ context.Context, _ string, call Call) (<-chan Reply, error) {
			calls++
			if call.Method != SessionToolMethod || call.Version != ToolSessionCapability.Version {
				t.Fatal(call)
			}
			replies := make(chan Reply, 1)
			replies <- Reply{Message: Envelope{Type: TypeResult, Result: &Result{Value: result}}}
			close(replies)
			return replies, nil
		})
	p := SessionToolCall{SessionID: "session-a", Context: "run-a", Tool: "lookup", Arguments: json.RawMessage(`{}`)}
	if _, err := b.Tool(t.Context(), d.ID, "tool-1", p); err != nil {
		t.Fatal(err)
	}
	grants = []string{SessionRunGrant, SessionPluginGrant}
	if _, err := b.Tool(t.Context(), d.ID, "tool-2", p); err == nil || calls != 1 {
		t.Fatal("revoked callback dispatched")
	}
	grants = d.RequestedGrants
	result = json.RawMessage(`null`)
	p.Arguments = json.RawMessage(`[]`)
	if _, err := b.Tool(t.Context(), d.ID, "tool-3", p); err == nil {
		t.Fatal("array arguments admitted")
	}
}
