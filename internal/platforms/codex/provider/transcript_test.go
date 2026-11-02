package codexprovider

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// TestRecordedTranscriptDecodes decodes every item the real CLI completed in
// the recorded session, so a field-shape mismatch fails here rather than at
// runtime, and checks each converts to a part.
func TestRecordedTranscriptDecodes(t *testing.T) {
	f, err := os.Open("testdata/0.164.0/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var items []codexappserver.Item
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 16<<20)
	for scanner.Scan() {
		var line struct {
			Dir string `json:"dir"`
			Msg struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			} `json:"msg"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Dir != "recv" || line.Msg.Method != "item/completed" {
			continue
		}
		var completed struct {
			Item codexappserver.Item `json:"item"`
		}
		if err := json.Unmarshal(line.Msg.Params, &completed); err != nil {
			t.Fatalf("item/completed: %v\n%s", err, line.Msg.Params)
		}
		items = append(items, completed.Item)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("recording has no completed items")
	}
	// Every completed item converts to a part, except the user's own messages.
	for _, it := range items {
		if it.Type != codexappserver.ItemUserMessage && ItemPart(it, 1) == nil {
			t.Errorf("item %s (%s) did not convert", it.ID, it.Type)
		}
	}
}
