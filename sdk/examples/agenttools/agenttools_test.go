package agenttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
	"github.com/ocman/ocman/sdk/protocol"
)

func TestExamplesDeclareValidDescriptions(t *testing.T) {
	for _, p := range []Plugin{Notes("org.example.notes"), Inventory("org.example.inventory")} {
		if err := p.Description.Validate(); err != nil {
			t.Fatalf("%s: %v", p.Description.ID, err)
		}
	}
}

func TestSupplierConfirmationsAcceptRepresentativeNames(t *testing.T) {
	p := Inventory("org.example.inventory")
	for _, name := range []string{"acme", "Acme Supplies Ltd", "Zulieferer Müller", "line\nbreak", strings.Repeat("x", 64)} {
		args, _ := json.Marshal(map[string]any{"operation": "supplier.connect", "arguments": map[string]any{"supplier": name, "token": "secret"}})
		inv, _ := json.Marshal(plugin.ToolInvocation{Tool: "inventory", Arguments: args,
			Context: plugin.ToolContext{Principal: "agent", Transport: "mcp", Grants: []string{plugin.ToolAgentGrant, "inventory.adjust"}}})
		raw, err := p.Handler(context.Background(), plugin.Call{Capability: "tool", Version: plugin.ToolCapability.Version, Method: plugin.ToolInvokeMethod, Params: inv}, nil)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		r, err := protocol.DecodeToolResult(raw)
		if err != nil || r.Confirmation == nil || strings.Contains(r.Confirmation.Text, "secret") || strings.Join(r.Confirmation.Redact, ",") != "/arguments/token" {
			t.Fatalf("%q: %s %v", name, raw, err)
		}
	}
}
