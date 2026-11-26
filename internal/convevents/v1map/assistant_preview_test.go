package v1map

import (
	"encoding/json"
	"testing"
)

func TestAssistantPreviewRequiresLiveVisibleAnchors(t *testing.T) {
	p := AssistantPreview{SessionID: "s", PreviousMessageID: "old", MaxBytes: 5}
	for _, tc := range []struct {
		name, frame, text string
		done              bool
	}{
		{name: "old message", frame: `{"type":"message.updated","properties":{"info":{"id":"old","role":"assistant","sessionID":"s"},"parts":[{"id":"p","type":"text","text":"old"}]}}`},
		{name: "user message", frame: `{"type":"message.updated","properties":{"info":{"id":"user","role":"user","sessionID":"s"},"parts":[{"id":"p","type":"text","text":"user"}]}}`},
		{name: "unknown role", frame: `{"type":"message.updated","properties":{"info":{"id":"a","sessionID":"s"},"parts":[{"id":"p","type":"text","text":"raw"}]}}`},
		{name: "acknowledged", frame: `{"type":"message.updated","acknowledged":true,"properties":{"info":{"id":"a","role":"assistant","sessionID":"s"},"parts":[{"id":"p","type":"text","text":"replay"}]}}`},
		{name: "valid message", frame: `{"type":"message.updated","properties":{"info":{"id":"a","role":"assistant","sessionID":"s"}}}`},
		{name: "unanchored delta", frame: `{"type":"message.part.delta","properties":{"messageID":"a","partID":"p","sessionID":"s","field":"text","delta":"lost"}}`},
		{name: "ignored text", frame: `{"type":"message.part.updated","properties":{"part":{"id":"i","messageID":"a","sessionID":"s","type":"text","ignored":true,"text":"skip"}}}`},
		{name: "visible text", frame: `{"type":"message.part.updated","properties":{"part":{"id":"p","messageID":"a","sessionID":"s","type":"text","text":"yo"}}}`, text: "yo"},
		{name: "tool delta", frame: `{"type":"message.part.delta","properties":{"messageID":"a","partID":"p","sessionID":"s","field":"state.output","delta":"no"}}`},
		{name: "delta", frame: `{"type":"message.part.delta","properties":{"messageID":"a","partID":"p","sessionID":"s","field":"text","delta":"!"}}`, text: "yo!"},
		{name: "stale snapshot", frame: `{"type":"message.part.updated","properties":{"part":{"id":"p","messageID":"a","sessionID":"s","type":"text","text":"yo"}}}`},
		{name: "over cap", frame: `{"type":"message.part.delta","properties":{"messageID":"a","partID":"p","sessionID":"s","field":"text","delta":"..."}}`},
		{name: "foreign idle", frame: `{"type":"session.idle","properties":{"sessionID":"other"}}`},
		{name: "owned idle", frame: `{"type":"session.idle","properties":{"sessionID":"s"}}`, done: true},
		{name: "next user prompt", frame: `{"type":"message.updated","properties":{"info":{"id":"next","role":"user","sessionID":"s"}}}`, done: true},
	} {
		id, text, done := p.Apply(Frame{Data: json.RawMessage(tc.frame)})
		if text != tc.text || done != tc.done || (tc.text != "" && id != "a") || (tc.text == "" && id != "") {
			t.Fatalf("%s: got %q %q done=%v", tc.name, id, text, done)
		}
	}
}
