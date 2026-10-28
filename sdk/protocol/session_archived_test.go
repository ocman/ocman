package protocol

import (
	"errors"
	"testing"
)

func TestSessionBrokerArchivedRequiresMinor3(t *testing.T) {
	for _, tc := range []struct {
		version Version
		ok      bool
	}{
		{SessionCapability.Version, false},
		{PluginSessionCapability.Version, false},
		{ArchivedSessionCapability.Version, true},
		{Version{Major: 1, Minor: 4}, false},
	} {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo"}
		call := sessionRunEvent(t, SessionRunParams{Prompt: "verify", Archived: true})
		call.Version = tc.version
		_, err := h.broker().Run(t.Context(), "org.example.run", call)
		if tc.ok != (err == nil) || tc.ok != (len(h.started) == 1) {
			t.Fatalf("version %+v: err=%v started=%v", tc.version, err, h.started)
		}
		var wire *WireError
		if !tc.ok && !errors.As(err, &wire) {
			t.Fatalf("version %+v: unexpected error %v", tc.version, err)
		}
	}
}
