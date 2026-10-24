package v1map

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/convevents"
)

var update = flag.Bool("update", false, "rewrite the .oce.jsonl goldens")

const goldenNow = int64(1700000000000)

// fixtureLine is one line of a .v1.jsonl fixture: a frame, or a header
// naming the page session.
type fixtureLine struct {
	Session string `json:"session,omitempty"`
	Frame
}

func readFixture(t testing.TB, raw []byte) (string, []Frame) {
	var session string
	var frames []Frame
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l fixtureLine
		if err := json.Unmarshal(line, &l); err != nil {
			if t != nil {
				t.Fatalf("fixture line %s: %v", line, err)
			}
			frames = append(frames, Frame{Data: append(json.RawMessage(nil), line...)})
			continue
		}
		if l.Data == nil && l.Session != "" {
			session = l.Session
			continue
		}
		frames = append(frames, l.Frame)
	}
	return session, frames
}

// mapFixture maps frames and frames the events as envelopes, the way the
// hub will number them. Encoding fills each event's registered durability.
func mapFixture(session, platform string, frames []Frame) []convevents.Envelope {
	m := New(Options{SessionID: session, Now: func() int64 { return goldenNow }})
	var out []convevents.Envelope
	for _, f := range frames {
		r := m.Map(f)
		for _, ev := range r.Events {
			out = append(out, convevents.Envelope{V: convevents.Version, Epoch: "golden", Seq: uint64(len(out) + 1),
				SessionID: session, Platform: platform, At: goldenNow,
				Replayed: r.Replayed, Event: ev})
		}
	}
	return out
}

func encodeJSONL(t testing.TB, envs []convevents.Envelope) []byte {
	var b bytes.Buffer
	for _, env := range envs {
		line, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal %s: %v", env.Event.EventType(), err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func TestGolden(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*", "*.v1.jsonl"))
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, in := range inputs {
		platform := filepath.Base(filepath.Dir(in))
		name := platform + "/" + strings.TrimSuffix(filepath.Base(in), ".v1.jsonl")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			session, frames := readFixture(t, raw)
			if session == "" {
				t.Fatal(`fixture has no {"session": ...} header`)
			}
			envs := mapFixture(session, platform, frames)
			got := encodeJSONL(t, envs)
			if again := encodeJSONL(t, mapFixture(session, platform, frames)); !bytes.Equal(got, again) {
				t.Fatal("mapping is not deterministic")
			}
			checkWellFormed(t, got)
			out := strings.TrimSuffix(in, ".v1.jsonl") + ".oce.jsonl"
			if *update {
				if err := os.WriteFile(out, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/convevents/v1map -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s is stale; run go test ./internal/convevents/v1map -update and review the diff\ngot:\n%s", out, got)
			}
		})
	}
}

// checkWellFormed decodes every envelope line back to a registered event
// with a durability, and checks delta offsets are contiguous.
func checkWellFormed(t testing.TB, jsonl []byte) {
	t.Helper()
	fields := map[string]int{}
	partsOf := map[string][]string{}
	forget := func(partID string) {
		delete(fields, partID+"\x00"+convevents.FieldText)
		delete(fields, partID+"\x00"+convevents.FieldToolOutput)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(jsonl), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var env convevents.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			t.Fatalf("envelope %s: %v", line, err)
		}
		if _, unknown := env.Event.(convevents.Unknown); unknown {
			t.Fatalf("mapper produced unregistered type %q", env.Event.EventType())
		}
		if env.V != convevents.Version || env.Durability == "" || env.SessionID == "" {
			t.Fatalf("bad envelope %s", line)
		}
		again, err := json.Marshal(env)
		if err != nil || !bytes.Equal(again, line) {
			t.Fatalf("envelope does not round-trip:\n%s\n%s", line, again)
		}
		switch ev := env.Event.(type) {
		case convevents.PartUpserted:
			if !bytes.HasPrefix(bytes.TrimSpace(ev.Part.Data), []byte("{")) {
				t.Fatalf("part data is not an object: %s", line)
			}
			partsOf[ev.Part.MessageID] = append(partsOf[ev.Part.MessageID], ev.Part.ID)
			if ev.Final {
				forget(ev.Part.ID)
			}
		case convevents.PartRemoved:
			forget(ev.PartID)
		case convevents.MessageRemoved:
			for _, id := range partsOf[ev.MessageID] {
				forget(id)
			}
		case convevents.PartDelta:
			key := ev.PartID + "\x00" + ev.Field
			if seen, ok := fields[key]; ok && ev.Offset < seen {
				t.Fatalf("delta offset %d went backwards from %d: %s", ev.Offset, seen, line)
			}
			if ev.Offset < 0 || ev.Text == "" {
				t.Fatalf("bad delta %s", line)
			}
			fields[key] = ev.Offset + convevents.UTF16Len(ev.Text)
		}
	}
}
