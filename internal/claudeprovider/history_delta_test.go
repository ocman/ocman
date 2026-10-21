package claudeprovider

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestHistoryDeltaResumesAtLastMessage(t *testing.T) {
	index := openSynced(t, fixture(t))
	h := &HistoryReader{index: index}
	delta, ok := any(h).(interface {
		Delta(context.Context, string, string) (*db.HistorySnapshot, string, error)
	})
	if !ok {
		t.Fatal("history reader has no resumable transcript reads")
	}
	first, cursor, err := delta.Delta(t.Context(), mainID, "")
	if err != nil || cursor == "" || first.Partial {
		t.Fatalf("initial snapshot: %+v %q %v", first, cursor, err)
	}
	idle, same, err := delta.Delta(t.Context(), mainID, cursor)
	if err != nil || same != cursor || !idle.Partial || len(idle.Messages) != 0 || len(idle.Parts) != 0 {
		t.Fatalf("unchanged transcript produced content: %+v %v", idle, err)
	}
	appendTranscript(t, index, mainID, `{"type":"user","uuid":"new-user","timestamp":"2026-10-22T10:00:00Z","message":{"role":"user","content":"new prompt"}}`)
	after, next, err := delta.Delta(t.Context(), mainID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Partial || len(after.Messages) != 2 || next == cursor {
		t.Fatalf("expected last message plus appended prompt, got %d messages partial=%v cursorChanged=%v", len(after.Messages), after.Partial, next != cursor)
	}
}

func TestHistoryDeltaMatchesFullConversionForAppendedToolResults(t *testing.T) {
	index := openSynced(t, fixture(t))
	h := &HistoryReader{index: index}
	_, cursor, err := h.Delta(t.Context(), mainID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{
		`{"type":"user","uuid":"new-user","timestamp":"2026-10-22T10:00:00Z","message":{"role":"user","content":"run a tool"}}`,
		`{"type":"assistant","uuid":"a-line","timestamp":"2026-10-22T10:00:01Z","message":{"id":"new-assistant","role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"pwd"}}],"stop_reason":"tool_use"}}`,
		`{"type":"user","uuid":"result-line","timestamp":"2026-10-22T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"/repo"}]}}`,
		`{"type":"custom-title","customTitle":"Updated title"}`,
	} {
		appendTranscript(t, index, mainID, record)
		delta, next, err := h.Delta(t.Context(), mainID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		full, err := h.Snapshot(t.Context(), mainID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(delta.Messages, full.Messages[delta.MessageOffset:]) || !reflect.DeepEqual(delta.Session, full.Session) {
			t.Fatalf("tail conversion differs from full conversion after %s", record)
		}
		ids := map[string]bool{}
		for _, m := range delta.Messages {
			ids[m.ID] = true
		}
		var want []db.Part
		for _, p := range full.Parts {
			if ids[p.MessageID] {
				want = append(want, p)
			}
		}
		if !reflect.DeepEqual(delta.Parts, want) {
			t.Fatal("tail lost or changed an appended tool result")
		}
		cursor = next
	}
}

func TestHistoryDeltaFallsBackForInvalidOrReplacedSource(t *testing.T) {
	for _, mode := range []string{"bad-cursor", "truncate", "rewrite"} {
		t.Run(mode, func(t *testing.T) {
			index := openSynced(t, fixture(t))
			h := &HistoryReader{index: index}
			_, cursor, err := h.Delta(t.Context(), mainID, "")
			if err != nil {
				t.Fatal(err)
			}
			e, err := index.get(t.Context(), mainID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "bad-cursor":
				cursor = "invalid"
			case "truncate":
				if err := os.WriteFile(e.Path, []byte("{\"type\":\"user\",\"uuid\":\"replacement\",\"message\":{\"content\":\"replacement\"}}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "rewrite":
				raw, err := os.ReadFile(e.Path)
				if err != nil {
					t.Fatal(err)
				}
				// Harmless whitespace changes the prefix and grows the file.
				if err := os.WriteFile(e.Path, append([]byte(" \n"), raw...), 0600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, _, err := h.Delta(t.Context(), mainID, cursor)
			if err != nil || snapshot.Partial || snapshot.MessageOffset != 0 {
				t.Fatalf("source replacement reused a stale cursor: %+v %v", snapshot, err)
			}
		})
	}
}

func TestHistoryDeltaLeavesPartialLineForNextRead(t *testing.T) {
	index := openSynced(t, fixture(t))
	h := &HistoryReader{index: index}
	_, cursor, err := h.Delta(t.Context(), mainID, "")
	if err != nil {
		t.Fatal(err)
	}
	e, err := index.get(t.Context(), mainID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(e.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"type":"user","uuid":"later","message":{"content":"pending`); err != nil {
		t.Fatal(err)
	}
	partial, next, err := h.Delta(t.Context(), mainID, cursor)
	if err != nil || len(partial.Messages) != 1 {
		t.Fatalf("partial record was published: %+v %v", partial, err)
	}
	if _, err := f.WriteString("\"}}\n"); err != nil {
		t.Fatal(err)
	}
	completed, _, err := h.Delta(t.Context(), mainID, next)
	if err != nil || len(completed.Messages) != 2 || completed.Messages[1].ID != "later" {
		t.Fatalf("completed trailing record was skipped: %+v %v", completed, err)
	}
}
