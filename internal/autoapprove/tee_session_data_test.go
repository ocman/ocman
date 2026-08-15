package autoapprove

import (
	"bytes"
	"testing"
)

// TestTeeSessionDataChanged covers the events that mutate a session's
// stored messages/parts (which the session list aggregates over) and
// session deletion. Each must name the affected session so the snapshot
// cache can recompute just that row.
//
// The last case is the fail-safe: a payload that does not identify a
// session fires with an empty ID, which the consumer must treat as "the
// whole list is stale" rather than guess at an owner.
func TestTeeSessionDataChanged(t *testing.T) {
	tests := []struct {
		name        string
		sseData     string
		wantSession string
		wantFired   bool
	}{
		{
			name:        "message.updated via info",
			sseData:     "data: " + `{"type":"message.updated","properties":{"info":{"id":"msg-1","sessionID":"ses-1","role":"assistant"}}}` + "\n\n",
			wantSession: "ses-1",
			wantFired:   true,
		},
		{
			name:        "message.removed via properties",
			sseData:     "data: " + `{"type":"message.removed","properties":{"sessionID":"ses-2","messageID":"msg-2"}}` + "\n\n",
			wantSession: "ses-2",
			wantFired:   true,
		},
		{
			name:        "message.part.updated via part",
			sseData:     "data: " + `{"type":"message.part.updated","properties":{"part":{"id":"prt-1","sessionID":"ses-3","messageID":"msg-3"}}}` + "\n\n",
			wantSession: "ses-3",
			wantFired:   true,
		},
		{
			name:        "message.part.removed via properties",
			sseData:     "data: " + `{"type":"message.part.removed","properties":{"sessionID":"ses-4","partID":"prt-4"}}` + "\n\n",
			wantSession: "ses-4",
			wantFired:   true,
		},
		{
			name:        "message.part.delta via part",
			sseData:     "data: " + `{"type":"message.part.delta","properties":{"part":{"sessionID":"ses-5"}}}` + "\n\n",
			wantSession: "ses-5",
			wantFired:   true,
		},
		{
			name:        "session.deleted via info id",
			sseData:     "data: " + `{"type":"session.deleted","properties":{"info":{"id":"ses-6"}}}` + "\n\n",
			wantSession: "ses-6",
			wantFired:   true,
		},
		{
			name:        "session.deleted via sessionID",
			sseData:     "data: " + `{"type":"session.deleted","properties":{"sessionID":"ses-7"}}` + "\n\n",
			wantSession: "ses-7",
			wantFired:   true,
		},
		{
			name:        "unattributable payload fires with no session",
			sseData:     "data: " + `{"type":"message.updated","properties":{"info":{"id":"msg-9"}}}` + "\n\n",
			wantSession: "",
			wantFired:   true,
		},
		{
			name:      "unrelated event does not fire",
			sseData:   "data: " + `{"type":"server.connected","properties":{}}` + "\n\n",
			wantFired: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			var fired bool
			tee := &Tee{
				W: &bytes.Buffer{},
				OnSessionDataChanged: func(sessionID string) {
					got, fired = sessionID, true
				},
			}
			if _, err := tee.Write([]byte(tc.sseData)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if fired != tc.wantFired {
				t.Fatalf("fired = %v, want %v", fired, tc.wantFired)
			}
			if got != tc.wantSession {
				t.Errorf("session = %q, want %q", got, tc.wantSession)
			}
		})
	}
}

// TestTeeSessionDataChangedGlobalEnvelope pins that the /global/event
// wrapper is unwrapped for the new events too, since that is the stream
// the headless watcher actually reads.
func TestTeeSessionDataChangedGlobalEnvelope(t *testing.T) {
	var got string
	tee := &Tee{
		W:                    &bytes.Buffer{},
		OnSessionDataChanged: func(sessionID string) { got = sessionID },
	}
	data := "data: " + `{"directory":"/repo","payload":{"type":"message.updated","properties":{"info":{"sessionID":"ses-g"}}}}` + "\n\n"
	if _, err := tee.Write([]byte(data)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got != "ses-g" {
		t.Errorf("session = %q, want ses-g", got)
	}
}
