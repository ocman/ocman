package codexprovider

import (
	"fmt"
	"github.com/klauspost/compress/zstd"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRolloutUsageCacheDoesNotRereadPrefix(t *testing.T) {
	data, err := os.ReadFile(extras + "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var cache rolloutUsageCache
	if _, err := cache.read(path); err != nil {
		t.Fatal(err)
	}
	before := cache.bytes
	if _, err := cache.read(path); err != nil {
		t.Fatal(err)
	}
	if cache.bytes != before {
		t.Fatalf("unchanged rollout reread %d bytes", cache.bytes-before)
	}
	tail := []byte("{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"new\",\"model_context_window\":12345}}\n")
	if err := os.WriteFile(path, append(data, tail...), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := cache.read(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ReadRolloutUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental usage differs: got %v want %v", got, want)
	}
	if cache.bytes-before != int64(len(tail)) {
		t.Fatalf("append parsed %d bytes, want only %d", cache.bytes-before, len(tail))
	}
}

func TestCachedHistorySnapshotAndVerificationUseDifferentAccountingPaths(t *testing.T) {
	thread := load[codexappserver.ThreadReadResponse](t, extras+"thread-read.json").Thread
	data, err := os.ReadFile(extras + "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	thread.Path = filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(thread.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	f := &fakeHistory{threads: []*codexappserver.Thread{&thread}, turns: load[struct{ Data []codexappserver.Turn }](t, extras+"turns.json").Data, items: load[struct{ Data []codexappserver.ItemEntry }](t, extras+"items.json").Data}
	h := &HistoryReader{Client: f}
	first, err := h.CachedSnapshot(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := h.usage.bytes
	second, err := h.CachedSnapshot(t.Context(), thread.ID)
	if err != nil || h.usage.bytes != before || !reflect.DeepEqual(first, second) {
		t.Fatalf("unchanged snapshot reparsed rollout: %v", err)
	}
	verified, err := h.Snapshot(t.Context(), thread.ID)
	if err != nil || h.usage.bytes <= before || !reflect.DeepEqual(first, verified) {
		t.Fatalf("verification reused accounting cache: %v", err)
	}
}

func TestRolloutUsageCacheRebuildsAndPreservesPartialRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	line := []byte("{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"one\",\"model_context_window\":12345}}\n")
	var cache rolloutUsageCache
	for _, data := range [][]byte{line, line[:len(line)/2], line} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := cache.read(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == len(line) && got["one"].Window != 12345 {
			t.Fatalf("complete record lost: %v", got)
		}
		if len(data) < len(line) && len(got) != 0 {
			t.Fatalf("incomplete record accepted: %v", got)
		}
	}
	// Same-sized edits must rebuild, and explicit verification ignores hints.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := []byte(string(line))
	for i, b := range rewritten {
		if b == '1' {
			rewritten[i] = '9'
			break
		}
	}
	if err := os.WriteFile(path, rewritten, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	got, err := cache.readMode(path, true)
	if err != nil || got["one"].Window != 92345 {
		t.Fatalf("verification trusted cache: %v %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.read(path); err == nil {
		t.Fatal("missing rollout returned cached usage")
	}
	if err := os.WriteFile(path, line, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = cache.read(path)
	if err != nil || got["one"].Window != 12345 {
		t.Fatalf("replacement retained stale usage: %v %v", got, err)
	}
}

func TestRolloutUsageCacheIsBoundedAndSupportsCompressedFiles(t *testing.T) {
	data, err := os.ReadFile(extras + "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl.zst")
	if err := os.WriteFile(path, enc.EncodeAll(data, nil), 0600); err != nil {
		t.Fatal(err)
	}
	var cache rolloutUsageCache
	got, err := cache.read(path)
	if err != nil {
		t.Fatal(err)
	}
	checkRecordedUsage(t, got)
	before := cache.bytes
	if _, err := cache.read(path); err != nil || cache.bytes != before {
		t.Fatalf("unchanged compressed rollout reread: %v", err)
	}
	for i := range 65 {
		path := filepath.Join(dir, fmt.Sprintf("%d.jsonl", i))
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := cache.read(path); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.files) != 64 {
		t.Fatalf("usage cache grew to %d entries", len(cache.files))
	}
}
