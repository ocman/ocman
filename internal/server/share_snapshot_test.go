package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/share"
)

func TestTruncateShareParts_ShortensLongStringsKeepsStructure(t *testing.T) {
	huge := strings.Repeat("x", 40<<10)
	parts := []db.Part{{
		ID:        "p1",
		MessageID: "m1",
		Data: json.RawMessage(fmt.Sprintf(
			`{"type":"tool","tool":"read","state":{"status":"completed","input":{"filePath":"/a/b.go"},"output":%q}}`,
			huge)),
	}}

	got := truncateShareParts(parts, 1024)

	var decoded struct {
		Type  string `json:"type"`
		Tool  string `json:"tool"`
		State struct {
			Status string `json:"status"`
			Input  struct {
				FilePath string `json:"filePath"`
			} `json:"input"`
			Output string `json:"output"`
		} `json:"state"`
	}
	if err := json.Unmarshal(got[0].Data, &decoded); err != nil {
		t.Fatalf("truncated part is not valid JSON: %v", err)
	}
	// The fields the viewer renders from must survive.
	if decoded.Type != "tool" || decoded.Tool != "read" || decoded.State.Status != "completed" {
		t.Fatalf("structure lost: %+v", decoded)
	}
	if decoded.State.Input.FilePath != "/a/b.go" {
		t.Fatalf("file path lost: %q", decoded.State.Input.FilePath)
	}
	if len(decoded.State.Output) >= len(huge) {
		t.Fatal("output was not truncated")
	}
	if !strings.Contains(decoded.State.Output, "truncated for sharing") {
		t.Fatalf("no truncation marker: %q", decoded.State.Output[len(decoded.State.Output)-80:])
	}
}

func TestTruncateShareParts_LeavesShortPartsAlone(t *testing.T) {
	parts := []db.Part{{ID: "p1", Data: json.RawMessage(`{"type":"text","text":"hello"}`)}}
	got := truncateShareParts(parts, 1024)

	var decoded map[string]any
	if err := json.Unmarshal(got[0].Data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["text"] != "hello" {
		t.Fatalf("short text altered: %v", decoded["text"])
	}
}

func TestTruncateShareParts_PassesThroughUnparseableData(t *testing.T) {
	parts := []db.Part{{ID: "p1", Data: json.RawMessage(`not json`)}}
	got := truncateShareParts(parts, 8)
	if string(got[0].Data) != "not json" {
		t.Fatalf("opaque payload was mangled: %q", got[0].Data)
	}
}

func TestTruncateShareParts_TruncatesNestedArrays(t *testing.T) {
	huge := strings.Repeat("y", 5000)
	parts := []db.Part{{
		ID:   "p1",
		Data: json.RawMessage(fmt.Sprintf(`{"type":"tool","rows":[%q,%q]}`, huge, huge)),
	}}
	got := truncateShareParts(parts, 100)
	if len(got[0].Data) > 1000 {
		t.Fatalf("array strings not truncated, payload is %d bytes", len(got[0].Data))
	}
}

// TestSplitShareSnapshot_EveryChunkFitsBudget is the guard on the
// original failure: one oversized chunk made sharing impossible.
func TestSplitShareSnapshot_EveryChunkFitsBudget(t *testing.T) {
	var messages []db.Message
	var parts []db.Part
	for i := range 200 {
		id := fmt.Sprintf("m%d", i)
		messages = append(messages, db.Message{ID: id, SessionID: "s", Data: json.RawMessage(`{"role":"assistant"}`)})
		parts = append(parts, db.Part{
			ID: fmt.Sprintf("p%d", i), MessageID: id, SessionID: "s",
			Data: json.RawMessage(fmt.Sprintf(`{"type":"text","text":%q}`, strings.Repeat("z", 4000))),
		})
	}

	budget := 32 << 10
	chunks, err := splitShareSnapshot(&db.Session{ID: "s"}, messages, parts, budget)
	if err != nil {
		t.Fatalf("splitShareSnapshot: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected the snapshot to split, got %d chunk(s)", len(chunks))
	}
	for i, chunk := range chunks {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			t.Fatalf("marshal chunk %d: %v", i, err)
		}
		if len(encoded) > budget {
			t.Fatalf("chunk %d is %d bytes, over the %d budget", i, len(encoded), budget)
		}
	}
}

// TestSplitShareSnapshot_LosesNothing proves splitting is only a
// transport concern: the union of chunks is the original conversation.
func TestSplitShareSnapshot_LosesNothing(t *testing.T) {
	var messages []db.Message
	var parts []db.Part
	for i := range 50 {
		id := fmt.Sprintf("m%d", i)
		messages = append(messages, db.Message{ID: id, Data: json.RawMessage(`{"role":"user"}`)})
		parts = append(parts, db.Part{
			ID: fmt.Sprintf("p%d", i), MessageID: id,
			Data: json.RawMessage(fmt.Sprintf(`{"type":"text","text":%q}`, strings.Repeat("q", 2000))),
		})
	}

	chunks, err := splitShareSnapshot(&db.Session{ID: "s"}, messages, parts, 16<<10)
	if err != nil {
		t.Fatalf("splitShareSnapshot: %v", err)
	}

	gotMessages := map[string]bool{}
	gotParts := map[string]bool{}
	for _, chunk := range chunks {
		for _, m := range chunk.Messages {
			gotMessages[m.ID] = true
		}
		for _, p := range chunk.Parts {
			gotParts[p.ID] = true
		}
	}
	if len(gotMessages) != len(messages) {
		t.Fatalf("got %d messages across chunks, want %d", len(gotMessages), len(messages))
	}
	if len(gotParts) != len(parts) {
		t.Fatalf("got %d parts across chunks, want %d", len(gotParts), len(parts))
	}
}

func TestSplitShareSnapshot_SessionOnlyInFirstChunk(t *testing.T) {
	chunks, err := splitShareSnapshot(&db.Session{ID: "s"}, nil, nil, 16<<10)
	if err != nil {
		t.Fatalf("splitShareSnapshot: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks for an empty conversation, want 1", len(chunks))
	}
	if chunks[0].Session == nil || chunks[0].Session.ID != "s" {
		t.Fatal("chunk 0 must carry the session")
	}
	if chunks[0].Messages == nil || chunks[0].Parts == nil {
		t.Fatal("empty collections must encode as [] rather than null")
	}
}

func TestSplitShareSnapshot_RejectsBadBudget(t *testing.T) {
	if _, err := splitShareSnapshot(nil, nil, nil, 0); err == nil {
		t.Fatal("expected an error for a zero budget")
	}
}

func TestShareChunkBudget(t *testing.T) {
	if got := shareChunkBudget(1 << 20); got >= 1<<20 {
		t.Fatalf("budget %d must leave headroom under the relay limit", got)
	}
	// A relay advertising nothing, or something tiny, still yields a
	// usable floor rather than a zero or negative budget.
	if got := shareChunkBudget(0); got <= 0 {
		t.Fatalf("budget = %d for an unknown limit", got)
	}
	if got := shareChunkBudget(64); got <= 0 {
		t.Fatalf("budget = %d for a tiny limit", got)
	}
}

func TestUploadChunksRejectsTooManyChunksBeforeUpload(t *testing.T) {
	s := &Server{}
	client := share.RelayClient{BaseURL: "http://127.0.0.1:1"}
	key, err := share.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	_, err = s.uploadChunks(context.Background(), client, share.RelayAllocation{
		ID: "share", MaxChunks: 1,
	}, key, 0, []relayChunk{{}, {}})
	var relayErr *share.RelayError
	if !errors.As(err, &relayErr) || relayErr.Status != 413 || relayErr.Message != "share has too many chunks" {
		t.Fatalf("error = %v, want a local share-too-large error", err)
	}
}
