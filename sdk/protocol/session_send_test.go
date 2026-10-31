package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSessionSendRequiresVersionAndGrant(t *testing.T) {
	h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}}
	h.description.Capabilities = []Capability{{Name: "session", Version: Version{Major: 1, Minor: 5}}}
	h.description.RequestedGrants = append(h.description.RequestedGrants, "session.send")
	b := h.broker()
	call := Call{OperationID: "followup-1", Capability: "session", Version: Version{Major: 1, Minor: 5}, Method: "send", Params: json.RawMessage(`{"after":"sess-out:1","prompt":"Clarify the supplied inventory"}`)}
	if _, err := b.Run(t.Context(), "org.example.run", call); err == nil {
		t.Fatal("send admitted without a grant or implementation")
	}
	h.grants = append(h.grants, "session.send")
	_, err := b.Run(t.Context(), "org.example.run", call)
	var wire *WireError
	if !errors.As(err, &wire) || wire.Category != ErrorUnavailable {
		t.Fatalf("supported send did not reach its unavailable implementation: %v", err)
	}
	call.Version.Minor = 4
	if _, err := b.Run(t.Context(), "org.example.run", call); err == nil {
		t.Fatal("send downgraded to an older host")
	}
}

func TestSessionSendReauthorizesAndPreservesParameters(t *testing.T) {
	h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant, SessionSendGrant}}
	h.description.Capabilities = []Capability{FollowupSessionCapability}
	h.description.RequestedGrants = append(h.description.RequestedGrants, SessionSendGrant)
	b := h.broker()
	sends := 0
	b.Continue = func(_ context.Context, id string, p SessionSendParams, operation string) (string, error) {
		sends++
		if id != "org.example.run" || p.After != "sess-out:1" || p.Prompt != "Clarify" || operation != "followup-1" {
			t.Fatalf("changed send: %s %+v %s", id, p, operation)
		}
		return "existing-session", nil
	}
	call := Call{OperationID: "followup-1", Capability: "session", Version: FollowupSessionCapability.Version, Method: SessionSendMethod, Params: json.RawMessage(`{"after":"sess-out:1","prompt":"Clarify"}`)}
	if result, err := b.Run(t.Context(), "org.example.run", call); err != nil || result.SessionID != "existing-session" || sends != 1 {
		t.Fatalf("send failed: %+v %v", result, err)
	}
	call.Version = AssociatedSessionCapability.Version
	h.description.Capabilities = []Capability{AssociatedSessionCapability}
	if result, err := b.Run(t.Context(), "org.example.run", call); err != nil || result.SessionID != "existing-session" || sends != 2 {
		t.Fatalf("send failed at association minor: %+v %v", result, err)
	}
	h.grants = []string{SessionRunGrant}
	if _, err := b.Run(t.Context(), "org.example.run", call); err == nil || sends != 2 {
		t.Fatal("revoked send grant did not block follow-up")
	}
	for _, data := range []string{`{"after":"sess-out:1","prompt":"Clarify","model":"override"}`, `{"after":"","prompt":"Clarify"}`, `{"after":"sess-out:1","prompt":" "}`} {
		call.Params = json.RawMessage(data)
		if _, err := b.Run(t.Context(), "org.example.run", call); err == nil {
			t.Fatalf("invalid send admitted: %s", data)
		}
	}
}
