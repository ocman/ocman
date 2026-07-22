package autoapprove

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestTeeWrappedResolutionCarriesDirectory(t *testing.T) {
	var got string
	tee := &Tee{
		W: &bytes.Buffer{},
		OnPromptResolved: func(directory, kind, sessionID, requestID string) {
			got = directory + "/" + kind + "/" + sessionID + "/" + requestID
		},
	}
	_, _ = tee.Write([]byte("data: {\"directory\":\"/repo/worktree\",\"payload\":{\"type\":\"question.rejected\",\"properties\":{\"sessionID\":\"ses-1\",\"requestID\":\"q-1\"}}}\n\n"))
	if got != "/repo/worktree/question/ses-1/q-1" {
		t.Fatalf("resolution = %q, want directory-qualified callback", got)
	}
}

func TestTeeNormalizesV2PermissionEvent(t *testing.T) {
	var got platforms.LivePrompt
	tee := &Tee{
		W:             &bytes.Buffer{},
		OnPromptAsked: func(_ string, _ string, prompt platforms.LivePrompt) { got = prompt },
	}
	_, _ = tee.Write([]byte(`data: {"directory":"/repo","payload":{"type":"permission.v2.asked","properties":{"id":"per-v2","sessionID":"ses-v2","action":"bash","resources":["git commit *"],"metadata":{}}}}` + "\n\n"))
	if got["permission"] != "bash" || !reflect.DeepEqual(got["patterns"], []string{"git commit *"}) {
		t.Fatalf("normalized prompt = %#v", got)
	}
}

// TestSsePermissionTeeQuestionAndIdle verifies the tee parses
// question.replied / question.rejected / session.idle events and fires
// the corresponding callbacks with the payload's sessionID (both
// casings) and request ID.
func TestSsePermissionTeeQuestionAndIdle(t *testing.T) {
	tests := []struct {
		name        string
		sseData     string
		wantKind    string // "question" | "idle"
		wantSession string
		wantRequest string
		wantReason  string
	}{
		{
			name: "question.replied envelope",
			sseData: "data: " +
				`{"type":"question.replied","properties":{"sessionID":"ses-1","requestID":"req-1"}}` +
				"\n\n",
			wantKind:    "question",
			wantSession: "ses-1",
			wantRequest: "req-1",
			wantReason:  "replied",
		},
		{
			name: "question.rejected named channel",
			sseData: "event: question.rejected\ndata: " +
				`{"type":"question.rejected","properties":{"sessionId":"ses-2","requestId":"req-2"}}` +
				"\n\n",
			wantKind:    "question",
			wantSession: "ses-2",
			wantRequest: "req-2",
			wantReason:  "rejected",
		},
		{
			name: "session.idle",
			sseData: "data: " +
				`{"type":"session.idle","properties":{"sessionID":"ses-3"}}` +
				"\n\n",
			wantKind:    "idle",
			wantSession: "ses-3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotKind, gotSession, gotRequest, gotReason string
			tee := &Tee{
				W: &bytes.Buffer{},
				OnQuestionResolved: func(sessionID, requestID, reason string) {
					gotKind, gotSession, gotRequest, gotReason = "question", sessionID, requestID, reason
				},
				OnSessionIdle: func(sessionID string) {
					gotKind, gotSession = "idle", sessionID
				},
			}
			if _, err := tee.Write([]byte(tc.sseData)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if gotKind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", gotKind, tc.wantKind)
			}
			if gotSession != tc.wantSession {
				t.Errorf("session = %q, want %q", gotSession, tc.wantSession)
			}
			if tc.wantKind == "question" {
				if gotRequest != tc.wantRequest {
					t.Errorf("request = %q, want %q", gotRequest, tc.wantRequest)
				}
				if gotReason != tc.wantReason {
					t.Errorf("reason = %q, want %q", gotReason, tc.wantReason)
				}
			}
		})
	}
}

// TestSsePermissionTeeSessionChanged verifies the tee parses
// session.updated events and fires onSessionChanged with the payload's
// sessionID (both casings, enveloped and flat).
func TestSsePermissionTeeSessionChanged(t *testing.T) {
	tests := []struct {
		name        string
		sseData     string
		wantSession string
	}{
		{
			name:        "envelope sessionID",
			sseData:     "data: " + `{"type":"session.updated","properties":{"sessionID":"ses-1","info":{"id":"ses-1"}}}` + "\n\n",
			wantSession: "ses-1",
		},
		{
			name:        "envelope sessionId lowercase",
			sseData:     "event: session.updated\ndata: " + `{"type":"session.updated","properties":{"sessionId":"ses-2"}}` + "\n\n",
			wantSession: "ses-2",
		},
		{
			name:        "flat shape",
			sseData:     "data: " + `{"type":"session.updated","sessionID":"ses-3"}` + "\n\n",
			wantSession: "ses-3",
		},
		{
			name:        "missing id fires nothing",
			sseData:     "data: " + `{"type":"session.updated","properties":{"info":{}}}` + "\n\n",
			wantSession: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			tee := &Tee{
				W:                &bytes.Buffer{},
				OnSessionChanged: func(sessionID string) { got = sessionID },
			}
			if _, err := tee.Write([]byte(tc.sseData)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if got != tc.wantSession {
				t.Errorf("session = %q, want %q", got, tc.wantSession)
			}
		})
	}
}
