package claudeprovider

import (
	"bytes"
	"github.com/ocman/ocman/internal/db"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIndexAppendResumesAfterRestart(t *testing.T) {
	config := fixture(t)
	path := filepath.Join(config, "projects", "-work-other", otherID+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append([]byte(`{"type":"file-history-snapshot","padding":"`+strings.Repeat("x", 1<<20)+"\"}\n"), raw...)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "index.db")
	x, err := OpenIndex(database, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}
	x, err = OpenIndex(database, config)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{\"type\":\"user\",\"uuid\":\"appended\",\"timestamp\":\"2026-11-02T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"next prompt\"}}\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := x.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := x.indexedBytes.Load(); n >= 8192 {
		t.Fatalf("append reparsed %d bytes, want only last message group and appended tail", n)
	}
	full := openSynced(t, config)
	got, err := x.query(t.Context(), "1=1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	want, err := full.query(t.Context(), "1=1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental index differs from complete reindex:\ngot %+v\nwant %+v", got, want)
	}
}

func TestIncrementalIndexMatchesFullAtEveryAppend(t *testing.T) {
	config := fixture(t)
	path := filepath.Join(config, "projects", "-work-repo", mainID+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	x := openSynced(t, config)
	var content []byte
	for i, line := range bytes.SplitAfter(raw, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		content = append(content, line...)
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		if err := x.Sync(t.Context()); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		assertIndexMatchesFull(t, x, path)
	}
	// Same-sized edits, truncation, and replacement must abandon old offsets.
	for _, data := range [][]byte{bytes.ReplaceAll(content, []byte("Marker"), []byte("Edited")), raw[:len(raw)/2], raw} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := x.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertIndexMatchesFull(t, x, path)
	}
}

func assertIndexMatchesFull(t *testing.T, x *Index, path string) {
	t.Helper()
	want, err := scanFile(path, x.projects)
	if err != nil {
		t.Fatal(err)
	}
	got, err := x.get(t.Context(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	offsets, err := x.offsets(t.Context(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offsets, want.offsets) {
		t.Fatalf("offsets differ: got %v want %v", offsets, want.offsets)
	}
	latest := map[string]db.AnalyticsMessage{}
	for _, u := range want.usage {
		latest[u.ID] = u
	}
	for _, u := range latest {
		var data, tools string
		var settled bool
		if err := x.db.QueryRow(`SELECT data,tools,settled FROM usage WHERE session_id=? AND id=?`, want.ID, u.ID).Scan(&data, &tools, &settled); err != nil {
			t.Fatal(err)
		}
		if data != u.Data || settled != u.Settled {
			t.Fatalf("usage %s differs: %s != %s", u.ID, data, u.Data)
		}
	}
	want.offsets, want.tasks, want.usage, want.readBytes = nil, nil, nil, 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection differs:\ngot %+v\nwant %+v", got, want)
	}
}

func TestIncrementalIndexFallsBackForReplayedHistoricalMessage(t *testing.T) {
	config := fixture(t)
	x := openSynced(t, config)
	path := filepath.Join(config, "projects", "-work-repo", mainID+".jsonl")
	for range 2 {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString("{\"type\":\"assistant\",\"timestamp\":\"2026-11-02T10:00:00Z\",\"message\":{\"id\":\"msg_A\",\"role\":\"assistant\",\"model\":\"claude-sonnet-5\",\"usage\":{\"input_tokens\":123,\"output_tokens\":7},\"content\":[{\"type\":\"text\",\"text\":\"replayed\"}]}}\n")
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := x.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertIndexMatchesFull(t, x, path)
	}
}

func TestIndexSkipsMalformedMetadataAndRecoversInvalidCheckpoint(t *testing.T) {
	config := fixture(t)
	x := openSynced(t, config)
	path := filepath.Join(config, "projects", "-work-repo", mainID+".jsonl")
	if _, err := x.db.Exec(`UPDATE index_cursor SET data='invalid checkpoint' WHERE path=?`, path); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{\"type\":\"custom-title\",\"customTitle\":\"corrupt title\",\"timestamp\":[]}\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := x.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	e, err := x.get(t.Context(), mainID)
	if err != nil || e.Title != "Marker subagent run" {
		t.Fatalf("malformed metadata changed the projection: %+v %v", e, err)
	}
}
