package historyjournal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func record(session, kind string) Record {
	return Record{Time: 1, Session: session, Type: kind, Data: json.RawMessage(`{"ok":true}`)}
}

func TestReadResumesFromCursorAndIgnoresIncompleteLines(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, "a.jsonl", record("s1", "one")); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, "a.jsonl", record("s1", "two")); err != nil {
		t.Fatal(err)
	}
	// A writer interrupted mid-line must not be consumed until it completes.
	f, err := os.OpenFile(filepath.Join(dir, "a.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"session":"s1","type":"thr`); err != nil {
		t.Fatal(err)
	}
	entries, err := Read(dir, nil, 100)
	if err != nil || len(entries) != 2 || entries[0].Record.Type != "one" || entries[1].Record.Type != "two" || entries[0].Record.V != Version {
		t.Fatalf("first read = %+v, %v", entries, err)
	}
	cursor := map[string]int64{"a.jsonl": entries[1].End}
	if again, err := Read(dir, cursor, 100); err != nil || len(again) != 0 {
		t.Fatalf("incomplete trailing line was consumed: %+v %v", again, err)
	}
	if _, err := f.WriteString("ee\"}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	next, err := Read(dir, cursor, 100)
	if err != nil || len(next) != 1 || next[0].Record.Type != "three" || next[0].Offset != entries[1].End {
		t.Fatalf("resume = %+v, %v", next, err)
	}
}

func TestReadLimitsAndOrdersSegments(t *testing.T) {
	dir := t.TempDir()
	for _, segment := range []string{"b.jsonl", "a.jsonl"} {
		for i := range 3 {
			if err := Append(dir, segment, record(segment, fmt.Sprint(i))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.tmp"), []byte("not a segment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Read(dir, nil, 4)
	if err != nil || len(entries) != 4 {
		t.Fatalf("limited read = %d, %v", len(entries), err)
	}
	if entries[0].Segment != "a.jsonl" || entries[3].Segment != "b.jsonl" || entries[3].Record.Type != "0" {
		t.Fatalf("segments out of order: %+v", entries)
	}
	if missing, err := Read(filepath.Join(dir, "absent"), nil, 10); err != nil || len(missing) != 0 {
		t.Fatalf("a journal that was never written is empty, not an error: %v %v", missing, err)
	}
	// A later segment is readable on its own; an earlier one never blocks it.
	if later, err := ReadSegment(dir, "b.jsonl", 0, 10); err != nil || len(later) != 3 || later[0].Segment != "b.jsonl" {
		t.Fatalf("segment read = %+v, %v", later, err)
	}
	if pruned, err := ReadSegment(dir, "gone.jsonl", 0, 10); err != nil || len(pruned) != 0 {
		t.Fatalf("pruned segment = %v, %v", pruned, err)
	}
	if _, err := ReadSegment(dir, "../escape.jsonl", 0, 10); err == nil {
		t.Fatal("read outside the journal")
	}
}

func TestReadReportsMalformedRecordsWithoutStalling(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte("{broken\n{\"v\":99,\"session\":\"s\",\"type\":\"x\"}\n{\"v\":1,\"session\":\"\",\"type\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, "a.jsonl", record("s", "good")); err != nil {
		t.Fatal(err)
	}
	entries, err := Read(dir, nil, 10)
	if err != nil || len(entries) != 4 {
		t.Fatalf("read = %+v, %v", entries, err)
	}
	for i, e := range entries[:3] {
		if e.Err == nil {
			t.Errorf("entry %d accepted an invalid record", i)
		}
	}
	if entries[3].Err != nil || entries[3].Record.Type != "good" {
		t.Fatalf("later valid record lost: %+v", entries[3])
	}
}

func TestReadDetectsTruncatedSegments(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, "a.jsonl", record("s", "x")); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir, map[string]int64{"a.jsonl": 1 << 20}, 10); !errors.Is(err, ErrGap) {
		t.Fatalf("truncated segment = %v", err)
	}
}

func TestConcurrentWritersNeverInterleaveRecords(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 50 {
				r := record(fmt.Sprintf("w%d", w), fmt.Sprint(i))
				r.Data = json.RawMessage(fmt.Sprintf(`{"padding":%q}`, strings.Repeat("x", 512)))
				if err := Append(dir, "shared.jsonl", r); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	entries, err := Read(dir, nil, 1000)
	if err != nil || len(entries) != 400 {
		t.Fatalf("read %d, %v", len(entries), err)
	}
	for _, e := range entries {
		if e.Err != nil {
			t.Fatalf("interleaved record: %v", e.Err)
		}
	}
}

func TestPruneRemovesOnlyConsumedIdleSegments(t *testing.T) {
	dir := t.TempDir()
	for _, segment := range []string{"done.jsonl", "partial.jsonl", "fresh.jsonl"} {
		if err := Append(dir, segment, record("s", "x")); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	for _, segment := range []string{"done.jsonl", "partial.jsonl"} {
		if err := os.Chtimes(filepath.Join(dir, segment), old, old); err != nil {
			t.Fatal(err)
		}
	}
	size := func(name string) int64 {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	cursors := map[string]int64{"done.jsonl": size("done.jsonl"), "partial.jsonl": 1, "fresh.jsonl": size("fresh.jsonl")}
	removed, err := Prune(dir, cursors, 10*time.Minute)
	if err != nil || len(removed) != 1 || removed[0] != "done.jsonl" {
		t.Fatalf("prune = %v, %v", removed, err)
	}
	for _, kept := range []string{"partial.jsonl", "fresh.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Fatalf("%s pruned before it was consumed and idle", kept)
		}
	}
}

func TestAppendRejectsUnsafeNamesAndRecords(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", "../escape.jsonl", "nested/a.jsonl", "a.txt"} {
		if err := Append(dir, name, record("s", "x")); err == nil {
			t.Errorf("accepted segment %q", name)
		}
	}
	if err := Append(dir, "a.jsonl", Record{Type: "x"}); err == nil {
		t.Error("accepted record without session")
	}
	// Conversation payloads are private: a created journal and its segments
	// must not be readable by other local users.
	private := filepath.Join(dir, "created")
	if err := Append(private, "a.jsonl", record("s", "x")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{private, filepath.Join(private, "a.jsonl")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s permissions = %v %v", path, info.Mode(), err)
		}
	}
}
