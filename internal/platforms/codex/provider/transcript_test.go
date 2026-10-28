package codexprovider

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// TestRecordedTranscriptDecodes decodes every server message of the recorded
// session into the typed protocol structs, so a field-shape mismatch with the
// real CLI fails here rather than at runtime.
func TestRecordedTranscriptDecodes(t *testing.T) {
	f, err := os.Open("testdata/0.164.0/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	typed := map[string]func() any{
		codexappserver.NotifyThreadStarted:       func() any { return &codexappserver.ThreadNotification{} },
		codexappserver.NotifyThreadStatusChanged: func() any { return &codexappserver.ThreadStatusNotification{} },
		codexappserver.NotifyThreadNameUpdated:   func() any { return &codexappserver.ThreadNameNotification{} },
		codexappserver.NotifyTokenUsageUpdated:   func() any { return &codexappserver.TokenUsageNotification{} },
		codexappserver.NotifyTurnStarted:         func() any { return &codexappserver.TurnNotification{} },
		codexappserver.NotifyTurnCompleted:       func() any { return &codexappserver.TurnNotification{} },
		codexappserver.NotifyItemStarted:         func() any { return &codexappserver.ItemNotification{} },
		codexappserver.NotifyItemCompleted:       func() any { return &codexappserver.ItemNotification{} },
		codexappserver.NotifyAgentMessageDelta:   func() any { return &codexappserver.DeltaNotification{} },
		codexappserver.NotifyServerRequestDone:   func() any { return &codexappserver.ServerRequestResolved{} },
		codexappserver.RequestCommandApproval:    func() any { return &codexappserver.CommandApprovalRequest{} },
		codexappserver.RequestFileChangeApproval: func() any { return &codexappserver.FileChangeApprovalRequest{} },
	}
	seen := map[string]int{}
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
		newValue := typed[line.Msg.Method]
		if line.Dir != "recv" || newValue == nil {
			continue
		}
		v := newValue()
		if err := json.Unmarshal(line.Msg.Params, v); err != nil {
			t.Fatalf("%s: %v\n%s", line.Msg.Method, err, line.Msg.Params)
		}
		seen[line.Msg.Method]++
		if n, ok := v.(*codexappserver.ItemNotification); ok && line.Msg.Method == codexappserver.NotifyItemCompleted {
			items = append(items, n.Item)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{codexappserver.NotifyTurnCompleted, codexappserver.NotifyItemCompleted, codexappserver.NotifyAgentMessageDelta,
		codexappserver.RequestCommandApproval, codexappserver.RequestFileChangeApproval, codexappserver.NotifyTokenUsageUpdated} {
		if seen[method] == 0 {
			t.Errorf("recording has no %s", method)
		}
	}
	// Every completed item the recording holds converts to a part, except
	// the user's own messages.
	for _, it := range items {
		if it.Type != codexappserver.ItemUserMessage && ItemPart(it, 1) == nil {
			t.Errorf("item %s (%s) did not convert", it.ID, it.Type)
		}
	}
}
