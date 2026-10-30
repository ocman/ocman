package db

import (
	"encoding/json"
	"testing"
)

func TestLatestAssistantTextVerbatim(t *testing.T) {
	messages := []Message{
		{ID: "progress", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant"}`)},
		{ID: "final", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant"}`)},
	}
	part := func(id, text string, timestamp int64) Part {
		data, err := json.Marshal(map[string]string{"type": "text", "text": text})
		if err != nil {
			t.Fatal(err)
		}
		return Part{MessageID: id, TimeCreated: timestamp, Data: data}
	}
	parts := []Part{
		part("final", "\n", 3), part("progress", "ignore progress", 0),
		part("final", " \t{\"text\":\"", 1), part("final", "界\\n\"}\n", 2),
		{MessageID: "final", Data: json.RawMessage(`{"type":"reasoning","text":"ignore reasoning"}`)},
	}
	if id, text := LatestAssistantTextVerbatim(messages, parts); id != "final" || text != " \t{\"text\":\"界\\n\"}\n\n" {
		t.Fatalf("changed structured text: %s %q", id, text)
	}
	// Equal timestamps preserve adapter arrival order, including whitespace.
	parts = []Part{part("final", "a\n", 0), part("final", "\t", 0), part("final", "b", 0)}
	if _, text := LatestAssistantTextVerbatim(messages, parts); text != "a\n\tb" {
		t.Fatalf("changed live part order: %q", text)
	}
	if id, text := LatestAssistantTextVerbatim(nil, parts); id != "" || text != "" {
		t.Fatal("invented assistant answer")
	}
}
