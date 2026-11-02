package claudeprovider

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func middleEditFixture(t *testing.T) (*Index, string, []byte) {
	t.Helper()
	config := t.TempDir()
	path := filepath.Join(config, "projects", "-repo", mainID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	padding := `{"type":"file-history-snapshot","padding":"` + strings.Repeat("x", 8192) + "\"}\n"
	middle := `{"type":"assistant","timestamp":"2026-11-02T10:00:00Z","message":{"id":"middle","role":"assistant","model":"claude-sonnet-5","stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":2},"content":[{"type":"text","text":"middle reply"}]}}` + "\n"
	last := `{"type":"user","uuid":"last","timestamp":"2026-11-02T10:00:01Z","message":{"role":"user","content":"next"}}` + "\n"
	raw := []byte(padding + middle + padding + last)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return openSynced(t, config), path, raw
}

func editMiddleAndAppend(t *testing.T, path string, raw []byte) {
	t.Helper()
	raw = bytes.ReplaceAll(raw, []byte(`"input_tokens":11`), []byte(`"input_tokens":99`))
	tail := `{"type":"assistant","timestamp":"2026-11-02T10:00:02Z","message":{"id":"new","role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":3,"output_tokens":1},"content":[{"type":"text","text":"new reply"}]}}` + "\n"
	if err := os.WriteFile(path, append(raw, tail...), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestIndexDetectsMiddleEditBeforeAppend(t *testing.T) {
	x, path, raw := middleEditFixture(t)
	editMiddleAndAppend(t, path, raw)
	if err := x.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertIndexMatchesFull(t, x, path)
}

func TestHistoryDeltaDetectsMiddleEditBeforeAppend(t *testing.T) {
	x, path, raw := middleEditFixture(t)
	h := &HistoryReader{index: x}
	_, cursor, err := h.Delta(t.Context(), mainID, "")
	if err != nil {
		t.Fatal(err)
	}
	editMiddleAndAppend(t, path, raw)
	snapshot, _, err := h.Delta(t.Context(), mainID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Partial || len(snapshot.Messages) != 3 {
		t.Fatalf("middle edit accepted as append-only: partial=%v messages=%d", snapshot.Partial, len(snapshot.Messages))
	}
}
