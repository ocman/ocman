package db

import (
	"encoding/json"
	"testing"
)

func TestArchivedOpenCodeMessageReplaysWithoutSource(t *testing.T) {
	ext := json.RawMessage(`{"metadata":{"role":"user"},"native":{"Type":"user","Data":{"text":"offline prompt","time":{"created":7}}}}`)
	message, supported, err := ReplayV2HistoryMessage("session", "message", ext)
	if err != nil || !supported || len(message.Parts) != 1 || message.Parts[0]["text"] != "offline prompt" {
		t.Fatalf("unexpected replay: %+v, %t, %v", message, supported, err)
	}
	if _, _, err := ReplayV2HistoryMessage("session", "message", json.RawMessage(`{"native":{"Type":"user"}}`)); err == nil {
		t.Fatal("expired raw message was treated as replayable")
	}
}
