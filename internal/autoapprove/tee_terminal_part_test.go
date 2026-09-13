package autoapprove

import "testing"

func TestTeeNormalizesTerminalBashParts(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		wantOutput string
	}{
		{"completed properties", `{"type":"message.part.updated","properties":{"part":{"id":"p1","messageID":"m1","sessionID":"s1","callID":"c1","type":"tool","tool":"bash","state":{"status":"completed","output":"[main abc1234] done"}}}}`, "[main abc1234] done"},
		{"errored data metadata output", `{"type":"message.part.updated","data":{"part":{"id":"p2","messageID":"m2","sessionID":"s2","callID":"c2","type":"tool","tool":"bash","state":{"status":"error","error":"interrupted","metadata":{"output":"[detached HEAD def5678] saved"}}}}}`, "[detached HEAD def5678] saved"},
		{"flat", `{"type":"message.part.updated","part":{"id":"p3","messageID":"m3","sessionID":"s3","callID":"c3","type":"tool","tool":"bash","state":{"status":"completed","output":"[main 123abcd] flat"}}}`, "[main 123abcd] flat"},
		{"named raw part", `{"id":"p4","messageID":"m4","sessionID":"s4","callID":"c4","type":"tool","tool":"bash","state":{"status":"completed","output":"[main 456defa] raw"}}`, "[main 456defa] raw"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got terminalPart
			tee := &Tee{OnTerminalPart: func(part terminalPart) { got = part }}
			eventType := ""
			if tt.name == "named raw part" {
				eventType = "message.part.updated"
			}
			tee.dispatchEvent(eventType, tt.json)
			if got.Output != tt.wantOutput || got.SessionID == "" || got.MessageID == "" || got.PartID == "" || got.CallID == "" {
				t.Fatalf("part = %#v", got)
			}
		})
	}
}

func TestTeeRejectsNonTerminalOrUnsupportedParts(t *testing.T) {
	for _, raw := range []string{
		`{"type":"message.part.updated","properties":{"part":{"type":"tool","tool":"read","state":{"status":"completed","output":"[main abc1234] nope"}}}}`,
		`{"type":"message.part.updated","properties":{"part":{"type":"tool","tool":"bash","state":{"status":"running","output":"[main abc1234] nope"}}}}`,
		`{"type":"message.part.updated","properties":{"part":{"type":"tool","tool":"bash","state":{"status":"completed"}}}}`,
	} {
		called := false
		tee := &Tee{OnTerminalPart: func(terminalPart) { called = true }}
		tee.dispatchEvent("", raw)
		if called {
			t.Fatalf("callback fired for %s", raw)
		}
	}
}
