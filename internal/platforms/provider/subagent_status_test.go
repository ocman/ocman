package provider

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestInterruptedChildStatusSurvivesIdleAcknowledgment(t *testing.T) {
	a := newFake(t, allCapabilities()).adapter(t)
	for i, event := range []providerprotocol.Event{
		{Type: "session.status", Properties: json.RawMessage(`{"sessionID":"child","status":{"type":"interrupted"}}`)},
		{Type: "session.idle", Properties: json.RawMessage(`{"sessionID":"child"}`)},
	} {
		if _, err := a.observe(uint64(i+1), event); err != nil {
			t.Fatal(err)
		}
	}
	if live := a.LiveStatus("child"); live == nil || live.Status != "interrupted" {
		t.Fatalf("interrupted child became successful after idle: %+v", live)
	}
	for i, event := range []providerprotocol.Event{
		{Type: "session.status", Properties: json.RawMessage(`{"sessionID":"child","status":{"type":"busy"}}`)},
		{Type: "session.idle", Properties: json.RawMessage(`{"sessionID":"child"}`)},
	} {
		if _, err := a.observe(uint64(i+3), event); err != nil {
			t.Fatal(err)
		}
	}
	if live := a.LiveStatus("child"); live.Status != "done" {
		t.Fatalf("interrupted status stuck across a new turn: %+v", live)
	}
}
