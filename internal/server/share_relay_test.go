package server

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestLatestCompletedTurnSelectsLatestUserTurnAndParts(t *testing.T) {
	messages := []db.Message{
		{ID: "u1", Data: json.RawMessage(`{"role":"user"}`)},
		{ID: "a1", Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)},
		{ID: "u2", Data: json.RawMessage(`{"role":"user"}`)},
		{ID: "a2", Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)},
	}
	parts := []db.Part{{ID: "p1", MessageID: "a1"}, {ID: "p2", MessageID: "u2"}, {ID: "p3", MessageID: "a2"}}
	chunk := latestCompletedTurn(&db.Session{ID: "s1"}, messages, parts)
	if len(chunk.Messages) != 2 || chunk.Messages[0].ID != "u2" || chunk.Messages[1].ID != "a2" {
		t.Fatalf("messages = %+v", chunk.Messages)
	}
	if len(chunk.Parts) != 2 || chunk.Parts[0].ID != "p2" || chunk.Parts[1].ID != "p3" {
		t.Fatalf("parts = %+v", chunk.Parts)
	}
}
