package pluginsmod

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/state/statetest"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func requireConfirmation(t *testing.T, m *Module, pluginID, tool string, args map[string]any) string {
	t.Helper()
	r := callTool(t, m, pluginID, tool, args)
	out := structured(t, r)
	id, _ := out["confirmationId"].(string)
	if r.IsError || out["status"] != "confirmation_required" || id == "" ||
		!strings.Contains(resultText(r), "[[ocman:card type=plugin-tool-confirmation plugin="+pluginID+" confirmation="+id+"]]") {
		t.Fatalf("expected confirmation: %s", resultText(r))
	}
	return id
}

func TestPluginAgentToolStaticConfirmationNeedsTheUser(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	startAgentToolPlugin(t, db, m, "notes", notesID, []string{plugins.ToolAgentGrant, "notes.write"})
	args := map[string]any{"id": "n1"}
	id := requireConfirmation(t, m, notesID, "notes_delete", args)

	report := toolsReport(t, m, notesID)
	if len(report.Confirmations) != 1 || report.Confirmations[0].Status != confirmationPending || report.Confirmations[0].Text != "Delete a saved note?" ||
		string(report.Confirmations[0].Arguments) != `{"id":"n1"}` {
		t.Fatalf("report %+v", report.Confirmations)
	}
	// The agent cannot proceed before the user decides.
	withID := map[string]any{"id": "n1", plugins.ToolConfirmationArgument: id}
	if r := callTool(t, m, notesID, "notes_delete", withID); !r.IsError || !strings.Contains(resultText(r), "has not approved") {
		t.Fatalf("pending resume: %s", resultText(r))
	}
	if err := decideConfirmation(t, m, notesID, id, "approve"); err != nil {
		t.Fatal(err)
	}
	if err := decideConfirmation(t, m, notesID, id, "deny"); !errors.Is(err, errPluginConflict) {
		t.Fatalf("second decision %v", err)
	}
	// Approval covers exactly the reviewed arguments.
	if r := callTool(t, m, notesID, "notes_delete", map[string]any{"id": "n2", plugins.ToolConfirmationArgument: id}); !r.IsError || !strings.Contains(resultText(r), "different arguments") {
		t.Fatalf("mismatch: %s", resultText(r))
	}
	if r := callTool(t, m, notesID, "notes_delete", withID); r.IsError || resultText(r) != "deleted n1" {
		t.Fatalf("confirmed: %s", resultText(r))
	}
	if r := callTool(t, m, notesID, "notes_delete", withID); !r.IsError || !strings.Contains(resultText(r), "was used") {
		t.Fatalf("reuse: %s", resultText(r))
	}
	// Revoking the tool's grant after approval refuses the resumed call.
	id = requireConfirmation(t, m, notesID, "notes_delete", map[string]any{"id": "n2"})
	if err := decideConfirmation(t, m, notesID, id, "approve"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginGrants(t.Context(), notesID, []string{plugins.ToolAgentGrant}); err != nil {
		t.Fatal(err)
	}
	if r := callTool(t, m, notesID, "notes_delete", map[string]any{"id": "n2", plugins.ToolConfirmationArgument: id}); !r.IsError || !strings.Contains(resultText(r), "not permitted") {
		t.Fatalf("revoked after approval: %s", resultText(r))
	}
	if err := db.SetPluginGrants(t.Context(), notesID, []string{plugins.ToolAgentGrant, "notes.write"}); err != nil {
		t.Fatal(err)
	}
	// A denied request never runs.
	id = requireConfirmation(t, m, notesID, "notes_delete", map[string]any{"id": "n2"})
	if err := decideConfirmation(t, m, notesID, id, "deny"); err != nil {
		t.Fatal(err)
	}
	if r := callTool(t, m, notesID, "notes_delete", map[string]any{"id": "n2", plugins.ToolConfirmationArgument: id}); !r.IsError || !strings.Contains(resultText(r), "was denied") {
		t.Fatalf("denied: %s", resultText(r))
	}
	if err := decideConfirmation(t, m, notesID, "tc_unknown", "approve"); err == nil {
		t.Fatal("unknown confirmation approved")
	}
	if err := decideConfirmation(t, m, notesID, id, "maybe"); err == nil {
		t.Fatal("invalid decision accepted")
	}
	if r := callTool(t, m, notesID, "notes_search", map[string]any{"query": "n", plugins.ToolConfirmationArgument: "tc_unknown"}); !r.IsError || !strings.Contains(resultText(r), "unknown confirmation_id") {
		t.Fatalf("unknown id: %s", resultText(r))
	}
}

func TestPluginAgentToolDynamicConfirmationAndRevocation(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	startAgentToolPlugin(t, db, m, "inventory", inventoryID, []string{plugins.ToolAgentGrant, "inventory.adjust"})
	small := map[string]any{"operation": "items.adjust", "arguments": map[string]any{"sku": "nut", "delta": -5}}
	if r := callTool(t, m, inventoryID, "inventory", small); r.IsError || structured(t, r)["quantity"] != float64(75) {
		t.Fatalf("small adjust: %s", resultText(r))
	}
	large := map[string]any{"operation": "items.adjust", "arguments": map[string]any{"sku": "bolt", "delta": 50}}
	id := requireConfirmation(t, m, inventoryID, "inventory", large)
	if report := toolsReport(t, m, inventoryID); report.Confirmations[0].Key != "adjust:bolt:50" || report.Confirmations[0].Text != "Adjust bolt stock by 50 units?" {
		t.Fatalf("report %+v", report.Confirmations)
	}
	if err := decideConfirmation(t, m, inventoryID, id, "approve"); err != nil {
		t.Fatal(err)
	}
	large[plugins.ToolConfirmationArgument] = id
	if r := callTool(t, m, inventoryID, "inventory", large); r.IsError || structured(t, r)["quantity"] != float64(170) {
		t.Fatalf("confirmed adjust: %s", resultText(r))
	}

	// An approval is stale once the grant it relied on is revoked.
	delete(large, plugins.ToolConfirmationArgument)
	id = requireConfirmation(t, m, inventoryID, "inventory", large)
	if err := decideConfirmation(t, m, inventoryID, id, "approve"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginGrants(t.Context(), inventoryID, []string{plugins.ToolAgentGrant}); err != nil {
		t.Fatal(err)
	}
	large[plugins.ToolConfirmationArgument] = id
	if r := callTool(t, m, inventoryID, "inventory", large); !r.IsError || !strings.Contains(resultText(r), "unknown operation items.adjust") {
		t.Fatalf("stale approval ran: %s", resultText(r))
	}
}

func TestToolConfirmationsExpireAndAreBounded(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	c := toolConfirmations{now: func() time.Time { return now }}
	d := plugins.Description{ID: "org.example.notes"}
	r, err := c.request(d.ID, "notes_delete", "", "Delete?", nil, []byte(`{}`), d, nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(toolConfirmationTTL)
	if err := c.decide(d.ID, r.ID, true, func(*toolConfirmation) error { return nil }); !errors.Is(err, errPluginConflict) {
		t.Fatalf("expired approval %v", err)
	}
	if got := c.list(d.ID); len(got) != 1 || got[0].Status != confirmationExpired {
		t.Fatalf("list %+v", got)
	}
	now = now.Add(toolConfirmationTTL + time.Second)
	if got := c.list(d.ID); len(got) != 0 {
		t.Fatalf("not forgotten %+v", got)
	}
	for range maxPendingToolConfirmation {
		if _, err := c.request(d.ID, "notes_delete", "", "Delete?", nil, []byte(`{}`), d, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.request(d.ID, "notes_delete", "", "Delete?", nil, []byte(`{}`), d, nil); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("cap %v", err)
	}
	c.revoke(d.ID)
	if got := c.list(d.ID); got[0].Status != confirmationExpired {
		t.Fatalf("revoke %+v", got[0])
	}
	// Approval is refused when the current authorization check fails.
	r, _ = c.request("org.example.other", "x", "", "Run?", nil, []byte(`{}`), d, nil)
	if err := c.decide("org.example.other", r.ID, true, func(*toolConfirmation) error { return plugins.ErrUnavailable }); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("authorization %v", err)
	}
}
