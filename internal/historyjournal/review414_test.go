package historyjournal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAfterInterruptedTailPreservesNextRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hook.jsonl"), []byte(`{"v":1`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, "hook.jsonl", record("s", "Stop")); err != nil {
		t.Fatal(err)
	}
	entries, err := Read(dir, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Err == nil && e.Record.Type == "Stop" {
			return
		}
	}
	t.Fatalf("valid event swallowed by interrupted tail: %+v", entries)
}
