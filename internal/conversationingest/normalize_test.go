package conversationingest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func historySnapshot(text string) *db.HistorySnapshot {
	return &db.HistorySnapshot{Session: db.Session{ID: "session", Title: "Conversation", Directory: "/repo", ParentID: "parent",
		TimeCreated: 1, TimeUpdated: 2}, Version: "v1",
		Messages: []db.Message{{ID: "message", SessionID: "session", TimeCreated: 1,
			Data: json.RawMessage(`{"role":"assistant","modelID":"model","providerID":"provider","time":{"completed":2},"tokens":{"input":9007199254740993}}`)}},
		Parts: []db.Part{{ID: "part", SessionID: "session", MessageID: "message", Data: json.RawMessage(`{"type":"text","text":` + string(mustJSON(text)) + `,"future":{"value":9007199254740993}}`)}},
	}
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestNormalizeRetainsContentAndIgnoresRuntimeState(t *testing.T) {
	snapshot := historySnapshot("Hello")
	snapshot.Session.Status = db.StatusBusy
	snapshot.Session.LiveConnection = true
	snapshot.NativeMessages = map[string]json.RawMessage{"message": json.RawMessage(`{"type":"assistant","data":{"unknown":true}}`)}
	batch, digest, err := Normalize(statev2.Key{InstanceID: "source", SessionID: "session"}, snapshot)
	if err != nil || len(digest) != 64 || !batch.Complete {
		t.Fatalf("normalize: %+v %q %v", batch, digest, err)
	}
	if batch.Messages[0].Model != "provider/model" || batch.Messages[0].CompletedAt != 2 || batch.Parts[0].Text != "Hello" {
		t.Fatalf("normalized content: %+v", batch)
	}
	if strings.Contains(string(batch.Session.Extensions), "busy") || strings.Contains(string(batch.Session.Extensions), "LiveConnection") {
		t.Fatal("runtime observations leaked into historical metadata")
	}
	if !strings.Contains(string(batch.Messages[0].Extensions), "9007199254740993") || !strings.Contains(string(batch.Messages[0].Extensions), `"native"`) {
		t.Fatal("native metadata or integer precision lost")
	}
	if strings.Contains(string(batch.Parts[0].Extensions), `"text"`) || !strings.Contains(string(batch.Parts[0].Extensions), "9007199254740993") {
		t.Fatal("text duplicated or unknown part fields lost")
	}
	snapshot.Messages[0].Data = json.RawMessage(`{"role":"user","model":{"modelID":"other","providerID":"p"}}`)
	snapshot.Parts[0].Data = json.RawMessage(`{"type":"future-block","attachment":{"url":"data:original"}}`)
	batch, _, err = Normalize(batch.Session.Key, snapshot)
	if err != nil || batch.Messages[0].Model != "p/other" || batch.Parts[0].Kind != "future-block" || !strings.Contains(string(batch.Parts[0].Extensions), "data:original") {
		t.Fatalf("unknown content lost: %+v %v", batch, err)
	}
}

func TestNormalizeCanonicalDigest(t *testing.T) {
	key := statev2.Key{InstanceID: "i", SessionID: "session"}
	a := historySnapshot("hello")
	b := historySnapshot("hello")
	b.Parts[0].Data = json.RawMessage(`{ "future": {"value":9007199254740993}, "text":"hello", "type":"text" }`)
	_, first, err := Normalize(key, a)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := Normalize(key, b)
	if err != nil || first != second {
		t.Fatalf("format-only differences changed digest: %s %s %v", first, second, err)
	}
}

func TestNormalizeRejectsIncompleteOrAmbiguousInput(t *testing.T) {
	for name, mutate := range map[string]func(*db.HistorySnapshot){
		"wrong session":         func(s *db.HistorySnapshot) { s.Session.ID = "other" },
		"empty message ID":      func(s *db.HistorySnapshot) { s.Messages[0].ID = "" },
		"duplicate message":     func(s *db.HistorySnapshot) { s.Messages = append(s.Messages, s.Messages[0]) },
		"cross session message": func(s *db.HistorySnapshot) { s.Messages[0].SessionID = "other" },
		"invalid message":       func(s *db.HistorySnapshot) { s.Messages[0].Data = json.RawMessage(`{`) },
		"missing role":          func(s *db.HistorySnapshot) { s.Messages[0].Data = json.RawMessage(`{}`) },
		"empty part ID":         func(s *db.HistorySnapshot) { s.Parts[0].ID = "" },
		"duplicate part":        func(s *db.HistorySnapshot) { s.Parts = append(s.Parts, s.Parts[0]) },
		"cross session part":    func(s *db.HistorySnapshot) { s.Parts[0].SessionID = "other" },
		"orphan part":           func(s *db.HistorySnapshot) { s.Parts[0].MessageID = "missing" },
		"missing kind":          func(s *db.HistorySnapshot) { s.Parts[0].Data = json.RawMessage(`{}`) },
		"invalid part":          func(s *db.HistorySnapshot) { s.Parts[0].Data = json.RawMessage(`{`) },
		"invalid native payload": func(s *db.HistorySnapshot) {
			s.NativeMessages = map[string]json.RawMessage{"message": json.RawMessage(`{`)}
		},
		"orphan native payload": func(s *db.HistorySnapshot) {
			s.NativeMessages = map[string]json.RawMessage{"missing": json.RawMessage(`{}`)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := historySnapshot("text")
			mutate(snapshot)
			if _, _, err := Normalize(statev2.Key{InstanceID: "i", SessionID: "session"}, snapshot); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
	if _, _, err := Normalize(statev2.Key{}, nil); err == nil {
		t.Fatal("accepted nil snapshot")
	}
	if _, _, err := Normalize(statev2.Key{SessionID: "session"}, historySnapshot("x")); err == nil {
		t.Fatal("accepted empty instance")
	}
	if _, err := canonical([]byte(`{`)); err == nil {
		t.Fatal("accepted invalid canonical JSON")
	}
}

// The adapter's subagent decision reaches the store unchanged, whatever the title.
func TestNormalizeKeepsAdapterSubagentDecision(t *testing.T) {
	for _, subagent := range []bool{false, true} {
		snapshot := historySnapshot("Hello")
		snapshot.Subagent = subagent
		batch, _, err := Normalize(statev2.Key{InstanceID: "source", SessionID: "session"}, snapshot)
		if err != nil || batch.Session.Subagent != subagent {
			t.Fatalf("subagent %v: %+v %v", subagent, batch.Session, err)
		}
	}
}
