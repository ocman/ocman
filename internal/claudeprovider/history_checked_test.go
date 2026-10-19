package claudeprovider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appendTranscript(t *testing.T, index *Index, id, record string) {
	t.Helper()
	entry, err := index.get(t.Context(), id)
	if err != nil || entry == nil {
		t.Fatalf("entry %s: %+v %v", id, entry, err)
	}
	f, err := os.OpenFile(entry.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(record + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := index.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHistorySnapshotRejectsUndecodableKnownRecords(t *testing.T) {
	for name, record := range map[string]string{
		"message":       `{"type":"user","uuid":"u-bad","timestamp":"2026-01-01T00:00:09Z","message":42}`,
		"content":       `{"type":"user","uuid":"u-bad","timestamp":"2026-01-01T00:00:09Z","message":{"role":"user","content":{"text":"x"}}}`,
		"content block": `{"type":"assistant","uuid":"a-bad","timestamp":"2026-01-01T00:00:09Z","message":{"id":"msg-bad","content":[{"type":"text","text":5}]}}`,
		"field":         `{"type":"assistant","uuid":"a-bad","timestamp":7,"message":{"id":"msg-bad","content":"x"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			index := openSynced(t, fixture(t))
			history := &HistoryReader{index: index}
			if _, err := history.Snapshot(t.Context(), mainID); err != nil {
				t.Fatalf("initial snapshot: %v", err)
			}
			appendTranscript(t, index, mainID, record)
			snapshot, err := history.Snapshot(t.Context(), mainID)
			if err == nil || snapshot != nil {
				t.Fatalf("undecodable record silently dropped: snapshot=%t err=%v", snapshot != nil, err)
			}
			if !strings.Contains(err.Error(), mainID) {
				t.Fatalf("error does not name the session: %v", err)
			}
		})
	}
}

func TestHistorySnapshotSkipsUnknownRecordTypes(t *testing.T) {
	index := openSynced(t, fixture(t))
	history := &HistoryReader{index: index}
	before, err := history.Snapshot(t.Context(), mainID)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{
		`{"type":"future-record","message":42}`,
		`{"type":"summary","summary":{"nested":true},"message":[]}`,
		`{"type":7}`,
		`[1,2]`,
		`{"type":"user","isMeta":true,"uuid":"meta","message":{"content":"<local-command-caveat>"}}`,
	} {
		appendTranscript(t, index, mainID, record)
	}
	after, err := history.Snapshot(t.Context(), mainID)
	if err != nil {
		t.Fatalf("ignored record types failed the snapshot: %v", err)
	}
	if len(after.Messages) != len(before.Messages) || len(after.Parts) != len(before.Parts) {
		t.Fatalf("ignored records changed history: %d/%d -> %d/%d", len(before.Messages), len(before.Parts), len(after.Messages), len(after.Parts))
	}
}

func TestConverterAddCheckedAcceptsTestdataTranscripts(t *testing.T) {
	var paths []string
	for _, pattern := range []string{"testdata/projects/*/*.jsonl", "testdata/projects/*/*/subagents/*.jsonl", "testdata/[0-9]*/*.jsonl"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("no testdata transcripts")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		checked, loose := &Converter{SessionID: "s"}, &Converter{SessionID: "s"}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if err := checked.AddChecked([]byte(line)); err != nil {
				t.Fatalf("%s:%d: %v", path, i+1, err)
			}
			loose.Add([]byte(line))
		}
		checked.Finish()
		loose.Finish()
		if len(checked.Messages) != len(loose.Messages) || len(checked.Parts) != len(loose.Parts) {
			t.Fatalf("%s: checked conversion diverged from Add", path)
		}
	}
}

func TestHistoryInventoryReportsNestedScanFailureAfterImport(t *testing.T) {
	config := fixture(t)
	index := openSynced(t, config)
	history := &HistoryReader{index: index}
	entries, err := history.Inventory(t.Context())
	if err != nil || len(entries) != 3 {
		t.Fatalf("initial inventory: %+v %v", entries, err)
	}
	child, err := index.get(t.Context(), childID)
	if err != nil || child == nil {
		t.Fatalf("child entry: %+v %v", child, err)
	}
	subagents := filepath.Dir(child.Path)
	if err := os.Rename(subagents, subagents+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subagents, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if entries, err := history.Inventory(t.Context()); err == nil {
		t.Fatalf("stale inventory returned as authoritative after a failed scan: %d entries", len(entries))
	}
}

// A Claude subagent transcript is marked a subagent by its location, not by
// a title convention.
func TestHistorySnapshotMarksSubagentTranscripts(t *testing.T) {
	history := &HistoryReader{index: openSynced(t, fixture(t))}
	for id, want := range map[string]bool{mainID: false, childID: true} {
		snapshot, err := history.Snapshot(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Subagent != want {
			t.Errorf("%s subagent = %v, want %v (title %q)", id, snapshot.Subagent, want, snapshot.Session.Title)
		}
	}
}
