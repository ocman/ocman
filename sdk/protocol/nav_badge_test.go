package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeNavBadgeClosedPayload(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"Kind":"dot"}`, `{"kind":"dot","kind":"clear"}`,
		`{"kind":"dot","count":0}`, `{"kind":"clear","count":null}`, `{"kind":"count"}`,
		`{"kind":"count","count":1.5}`, `{"kind":"count","count":"1"}`, `{"kind":"count","count":null}`,
		`{"kind":"count","count":1000000}`, `{"kind":"custom"}`, `{"kind":"dot","ownerId":"local"}`,
		`{"kind":"clear","pluginId":"org.example.other"}`, `{"kind":"dot","text":"meeting content"}`,
		`{"kind":"dot"}` + strings.Repeat(" ", 128),
	} {
		event := Event{Capability: NavBadgeCapability.Name, Name: NavBadgeSetEvent, Data: json.RawMessage(raw)}
		if _, err := DecodeNavBadge(event); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := DecodeNavBadge(Event{Capability: NavBadgeCapability.Name, Name: "move", Data: json.RawMessage(`{"kind":"dot"}`)}); err == nil {
		t.Error("accepted the wrong event name")
	}
}

func TestNavBadgeOptIn(t *testing.T) {
	d := *hello(ModeServe).Hello.Description
	d.Capabilities = append(d.Capabilities, NavBadgeCapability)
	if d.Validate() == nil {
		t.Fatal("declaration without requested grant accepted")
	}
	d.RequestedGrants = append(d.RequestedGrants, NavBadgeGrant)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	grant := []string{NavBadgeGrant}
	if d.NavBadgeAllowed(nil) || !d.NavBadgeAllowed(grant) {
		t.Fatal("badge did not require approval")
	}
	d.Capabilities = nil
	if d.NavBadgeAllowed(grant) {
		t.Fatal("grant without the capability was accepted")
	}
}
