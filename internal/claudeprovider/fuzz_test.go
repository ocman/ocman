package claudeprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// FuzzConvertTranscript checks the transcript converter's paging promise: a
// page that starts at an indexed message offset converts exactly like the
// same messages of a whole-file conversion, and the index agrees with the
// converter on the message count and the latest message.
func FuzzConvertTranscript(f *testing.F) {
	for _, p := range []string{
		"testdata/projects/-work-repo/11111111-1111-4111-8111-111111111111.jsonl",
		"testdata/projects/-work-repo/11111111-1111-4111-8111-111111111111/subagents/agent-a1.jsonl",
		"testdata/projects/-work-other/22222222-2222-4222-8222-222222222222.jsonl",
		"testdata/projects/-work-other/agent-old.jsonl",
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	ts := func(s int) string { return fmt.Sprintf(`"timestamp":"2026-10-05T10:00:0%dZ"`, s) }
	f.Add([]byte(strings.Join([]string{
		`{"type":"user","uuid":"u1",` + ts(1) + `,"message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1",` + ts(2) + `,"message":{"id":"msg_A","model":"m","content":[{"type":"text","text":"x"}]}}`,
		`{"type":"assistant","uuid":"a2",` + ts(3) + `,"message":{"id":"msg_A","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","uuid":"u2",` + ts(4) + `,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"out"}]}}`,
		`{"type":"user","uuid":"u3",` + ts(5) + `,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"},{"type":"text","text":"note"}]}}`,
		`{"type":"assistant","uuid":"a3",` + ts(6) + `,"message":{"id":"msg_A","stop_reason":"end_turn","content":[{"type":"text","text":"y"}]}}`,
		`{"type":"user","uuid":"u4","isMeta":true,` + ts(7) + `,"message":{"role":"user","content":"meta"}}`,
	}, "\n") + "\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Lines without a parseable timestamp fall back to time.Now, which
		// differs between reads.
		if len(data) > 1<<16 || !allTimestamped(data) {
			return
		}
		path := filepath.Join(t.TempDir(), "ses.jsonl")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		e, err := scanFile(path)
		if err != nil {
			t.Fatalf("scanFile: %v", err)
		}
		full := convertRange(t, path, 0, e.End)
		if len(full.Messages) != len(e.offsets) {
			t.Fatalf("index has %d messages, converter %d\n%s", len(e.offsets), len(full.Messages), dumpConversion(full))
		}
		if n := len(full.Messages); n > 0 && full.Messages[n-1].ID != e.LatestID {
			t.Fatalf("index latest id %q, converter's last message %q", e.LatestID, full.Messages[n-1].ID)
		}
		want := dumpConversion(full)
		if again := dumpConversion(convertRange(t, path, 0, e.End)); again != want {
			t.Fatalf("non-deterministic conversion")
		}
		for k := 1; k < len(e.offsets); k++ {
			head := convertRange(t, path, 0, e.offsets[k])
			tail := convertRange(t, path, e.offsets[k], e.End)
			paged := &Converter{
				Messages: append(append([]db.Message{}, head.Messages...), tail.Messages...),
				Parts:    append(append([]db.Part{}, head.Parts...), tail.Parts...),
			}
			if got := dumpConversion(paged); got != want {
				t.Fatalf("pages split at offset %d (message %d) differ\n--- whole\n%s--- paged\n%s", e.offsets[k], k, want, got)
			}
		}
	})
}

func allTimestamped(data []byte) bool {
	for _, raw := range bytes.Split(data, []byte("\n")) {
		var l line
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		if (l.Type == "user" || l.Type == "assistant") && l.millis() == 0 {
			return false
		}
	}
	return true
}

func convertRange(t *testing.T, path string, start, end int64) *Converter {
	c := &Converter{SessionID: "ses", Messages: []db.Message{}, Parts: []db.Part{}}
	if start < end {
		if err := (&Index{}).convert(path, start, end, c); err != nil {
			t.Fatalf("convert: %v", err)
		}
	}
	return c
}

func dumpConversion(c *Converter) string {
	var b strings.Builder
	for _, m := range c.Messages {
		fmt.Fprintf(&b, "M %s %d %s\n", m.ID, m.TimeCreated, m.Data)
	}
	for _, p := range c.Parts {
		fmt.Fprintf(&b, "P %s %s %d %s\n", p.ID, p.MessageID, p.TimeCreated, p.Data)
	}
	return b.String()
}
