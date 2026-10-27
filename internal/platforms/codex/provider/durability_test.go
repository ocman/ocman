package codexprovider

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestBoundedStructuredEvents(t *testing.T) {
	// Individual strings are short, but the structured item exceeds the limit.
	children := make([]any, 20000)
	for i := range children {
		children[i] = strings.Repeat("x", 256)
	}
	for _, kind := range []string{"part", "info"} {
		props := map[string]any{kind: map[string]any{"id": "a", "sessionID": "thr", "children": children}}
		event := boundedEvent("thr", "m:a", "message.updated", props)
		if len(event.Properties) >= providerprotocol.MaxEventBytes || !strings.Contains(string(event.Properties), "truncated") || !json.Valid(event.Properties) {
			t.Fatalf("invalid bounded %s event", kind)
		}
	}
}

func TestBoundStrings(t *testing.T) {
	text := strings.Repeat("あ", 100000)
	value := map[string]any{"text": text, "nested": []any{text, map[string]any{"output": "short"}}}
	boundStrings(value)
	if got := value["text"].(string); !utf8.ValidString(got) || !strings.HasSuffix(got, "[Codex output truncated]") || len(got) >= len(text) {
		t.Fatal("invalid UTF-8 truncation")
	}
	if got := value["nested"].([]any)[0]; got != "[Codex output truncated]" {
		t.Fatalf("array truncation = %v", got)
	}
}

func TestStaleSettlementDoesNotEndNewTurn(t *testing.T) {
	h := newHarness(t, scripted)
	h.b.settle("unknown", db.StatusDone, 1)
	h.start("thr")
	h.b.settle("thr", db.StatusDone, 2)
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	if !running {
		t.Fatal("stale settlement released admission")
	}
	h.b.settle("thr", db.StatusDone, 1)
	h.b.settle("thr", db.StatusDone, 1)
}
