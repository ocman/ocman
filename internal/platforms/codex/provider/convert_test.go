package codexprovider

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func load[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// addHandwritten feeds a full-read thread through AddHistory, the way a
// paged read delivers it: turns without items, and items as entries.
func addHandwritten(c *Converter, th codexappserver.Thread) {
	var turns []codexappserver.Turn
	var entries []codexappserver.ItemEntry
	for _, turn := range th.Turns {
		for _, it := range turn.Items {
			entries = append(entries, codexappserver.ItemEntry{TurnID: turn.ID, Item: it})
		}
		turn.Items = nil
		turns = append(turns, turn)
	}
	c.AddHistory(&th, turns, entries)
}

func decodeMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func partsOf(c *Converter, message string) []db.Part {
	var out []db.Part
	for _, p := range c.Parts {
		if p.MessageID == message {
			out = append(out, p)
		}
	}
	return out
}

func TestConvertThread(t *testing.T) {
	c := &Converter{}
	addHandwritten(c, load[codexappserver.Thread](t, "testdata/handwritten/thread.json"))

	var ids []string
	for _, m := range c.Messages {
		ids = append(ids, m.ID)
		if m.SessionID != "thr_1" {
			t.Errorf("message %s session = %q", m.ID, m.SessionID)
		}
	}
	if want := []string{"msg_u1", "msg_r1", "msg_u2", "msg_c2", "msg_u3", "msg_turn_3"}; !slices.Equal(ids, want) {
		t.Fatalf("messages = %q, want %q", ids, want)
	}

	user := partsOf(c, "msg_u1")
	if len(user) != 2 || user[0].ID != "prt_u1" || user[1].ID != "prt_u1_1" || user[0].TimeCreated != 1790000000000 {
		t.Fatalf("user parts = %+v", user)
	}
	if img := decodeMap(t, user[1].Data); img["type"] != "file" || img["mime"] != "image/jpeg" || img["url"] != "file:///tmp/shot.jpg" || img["filename"] != "shot.jpg" {
		t.Errorf("image part = %v", img)
	}

	reply := decodeMap(t, c.Messages[1].Data)
	if reply["role"] != "assistant" || reply["providerID"] != "openai" || reply["modelID"] != "gpt-5.5" || reply["finish"] != "stop" {
		t.Errorf("reply info = %v", reply)
	}
	if tm := reply["time"].(map[string]any); tm["created"] != float64(1790000001000) || tm["completed"] != float64(1790000010000) {
		t.Errorf("reply time = %v", tm)
	}
	parts := partsOf(c, "msg_r1")
	var types []string
	for _, p := range parts {
		d := decodeMap(t, p.Data)
		types = append(types, d["type"].(string)+":"+str(d["tool"]))
		if d["id"] != p.ID || d["messageID"] != "msg_r1" || d["sessionID"] != "thr_1" {
			t.Errorf("part ids = %v", d)
		}
	}
	if want := []string{"reasoning:", "tool:bash", "tool:apply_patch", "text:", "text:"}; !slices.Equal(types, want) {
		t.Fatalf("reply parts = %q", types)
	}
	bash := decodeMap(t, parts[1].Data)["state"].(map[string]any)
	if bash["status"] != "completed" || bash["output"] != "a.go\n" || bash["metadata"].(map[string]any)["exit"] != float64(0) ||
		bash["input"].(map[string]any)["command"] != "ls" {
		t.Errorf("bash state = %v", bash)
	}
	patch := decodeMap(t, parts[2].Data)["state"].(map[string]any)["input"].(map[string]any)["patchText"]
	if patch != "*** Begin Patch\n*** Update File: /repo/a.go\n@@ -1 +1 @@\n-old\n+new\n*** End Patch" {
		t.Errorf("patch = %q", patch)
	}

	interrupted := decodeMap(t, c.Messages[3].Data)
	if interrupted["error"].(map[string]any)["name"] != "MessageAbortedError" {
		t.Errorf("interrupted reply = %v", interrupted)
	}
	// Items without timestamps fall back to their turn's start.
	if c.Messages[3].TimeCreated != 1790000050000 {
		t.Errorf("fallback time = %d", c.Messages[3].TimeCreated)
	}
	running := decodeMap(t, partsOf(c, "msg_c2")[0].Data)["state"].(map[string]any)
	if running["status"] != "running" {
		t.Errorf("running state = %v", running)
	}
	// A failed turn with no reply item gets an assistant message, keyed by
	// the turn, that carries the failure.
	failed := decodeMap(t, c.Messages[5].Data)
	if failed["role"] != "assistant" || failed["providerID"] != "openai" ||
		failed["error"].(map[string]any)["data"].(map[string]any)["message"] != "usage limit reached" {
		t.Errorf("failed turn message = %v", failed)
	}
	if tm := failed["time"].(map[string]any); c.Messages[5].TimeCreated != 1790000070000 || tm["completed"] != float64(1790000070000) {
		t.Errorf("failed turn time = %d, %v", c.Messages[5].TimeCreated, tm)
	}
	if len(partsOf(c, "msg_turn_3")) != 0 {
		t.Errorf("failed turn message has parts")
	}
}

func TestFailedTurnAfterUserOnlyIsStable(t *testing.T) {
	turn := codexappserver.Turn{ID: "t", Status: codexappserver.TurnFailed, StartedAt: 3, CompletedAt: 4,
		Items: []codexappserver.Item{{Type: codexappserver.ItemUserMessage, ID: "u", StartedAtMs: 3500}}}
	stored, live := &Converter{SessionID: "s"}, &Converter{SessionID: "s"}
	stored.AddTurn(&turn)
	live.AddItem(turn.Items[0], 0)
	turn.Items = nil
	live.AddTurn(&turn)
	if len(stored.Messages) != 2 || stored.Messages[1].ID != "msg_t" || stored.Messages[1].TimeCreated != 4000 ||
		string(stored.Messages[1].Data) != string(live.Messages[1].Data) {
		t.Fatalf("stored = %+v\nlive = %+v", stored.Messages, live.Messages)
	}
	// A completed turn without a reply adds nothing.
	c := &Converter{SessionID: "s"}
	c.AddTurn(&codexappserver.Turn{ID: "ok", Status: codexappserver.TurnCompleted})
	if len(c.Messages) != 0 {
		t.Fatalf("messages = %+v", c.Messages)
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func TestConvertFailedTurnError(t *testing.T) {
	for _, tc := range []struct {
		err  *codexappserver.TurnError
		want string
	}{{&codexappserver.TurnError{Message: "quota"}, "quota"}, {nil, "Turn failed"}} {
		c := &Converter{SessionID: "s"}
		c.AddTurn(&codexappserver.Turn{Status: codexappserver.TurnFailed, Error: tc.err, Items: []codexappserver.Item{
			{Type: codexappserver.ItemAgentMessage, ID: "a", Text: "partial"}}})
		info := decodeMap(t, c.Messages[0].Data)
		if info["error"].(map[string]any)["data"].(map[string]any)["message"] != tc.want {
			t.Errorf("error = %v", info["error"])
		}
	}
}

func TestCompletedItemReplacesStarted(t *testing.T) {
	c := &Converter{SessionID: "s"}
	c.AddItem(codexappserver.Item{Type: codexappserver.ItemCommandExecution, ID: "c", Command: "ls", Status: "inProgress", StartedAtMs: 5}, 0)
	out := "x"
	c.AddItem(codexappserver.Item{Type: codexappserver.ItemCommandExecution, ID: "c", Command: "ls", Status: "completed", AggregatedOutput: &out, StartedAtMs: 5, CompletedAtMs: 9}, 0)
	c.Finish()
	c.Finish() // idempotent
	if len(c.Parts) != 1 || decodeMap(t, c.Parts[0].Data)["state"].(map[string]any)["status"] != "completed" {
		t.Fatalf("parts = %+v", c.Parts)
	}
	if tm := decodeMap(t, c.Messages[0].Data)["time"].(map[string]any); tm["completed"] != float64(9) {
		t.Errorf("time = %v", tm)
	}
}

func TestItemTimeFallsBackToCompletion(t *testing.T) {
	c := &Converter{SessionID: "s"}
	c.AddItem(codexappserver.Item{Type: codexappserver.ItemAgentMessage, ID: "a", CompletedAtMs: 9}, 1)
	c.Finish()
	if c.Messages[0].TimeCreated != 9 || c.Parts[0].TimeCreated != 9 {
		t.Fatalf("times = %d, %d", c.Messages[0].TimeCreated, c.Parts[0].TimeCreated)
	}
}

func TestUserParts(t *testing.T) {
	cases := []struct {
		in   codexappserver.UserInput
		mime string
	}{
		{codexappserver.UserInput{Type: "image", URL: "data:image/webp;base64,AA"}, "image/webp"},
		{codexappserver.UserInput{Type: "image", URL: "https://x/y"}, "image/png"},
		{codexappserver.UserInput{Type: "image", URL: "data:;base64,AA"}, "image/png"},
		{codexappserver.UserInput{Type: "localImage", Path: "/a/b.GIF"}, "image/gif"},
		{codexappserver.UserInput{Type: "localImage", Path: "/a/b.webp"}, "image/webp"},
		{codexappserver.UserInput{Type: "localImage", Path: "/a/b.png"}, "image/png"},
	}
	for _, tc := range cases {
		if p := userPart(tc.in); p["mime"] != tc.mime {
			t.Errorf("userPart(%+v) = %v", tc.in, p)
		}
	}
	if userPart(codexappserver.UserInput{Type: "skill", Name: "x"}) != nil {
		t.Error("skill input converted")
	}
}

func TestDefaultsDoNotOverride(t *testing.T) {
	c := &Converter{SessionID: "keep", ProviderID: "p", ModelID: "m"}
	c.AddHistory(&codexappserver.Thread{ID: "other", ModelProvider: "openai", Model: "gpt"}, nil, nil)
	if c.SessionID != "keep" || c.ProviderID != "p" || c.ModelID != "m" {
		t.Fatalf("converter = %+v", c)
	}
}

// TestRecordedHistory converts the paged reads recorded from Codex 0.164.0
// (codextest/record): an approved command, an apply_patch file creation and a
// turn interrupted by a cancelled approval, whose command is not stored.
func TestRecordedHistory(t *testing.T) {
	const dir = "testdata/0.164.0/"
	thread := load[codexappserver.ThreadReadResponse](t, dir+"thread-read.json").Thread
	turns := load[struct{ Data []codexappserver.Turn }](t, dir+"turns.json").Data
	items := load[struct{ Data []codexappserver.ItemEntry }](t, dir+"items.json").Data
	c := &Converter{}
	c.AddHistory(&thread, turns, items)

	var roles []string
	for _, m := range c.Messages {
		roles = append(roles, decodeMap(t, m.Data)["role"].(string))
	}
	if !slices.Equal(roles, []string{"user", "assistant", "user", "assistant"}) {
		t.Fatalf("roles = %q", roles)
	}
	if c.Messages[0].ID != "msg_"+items[0].Item.ID || c.Messages[1].ID != "msg_"+items[1].Item.ID {
		t.Fatalf("message ids = %s, %s", c.Messages[0].ID, c.Messages[1].ID)
	}
	// Stored entries carry the same timestamps live notifications do.
	if c.Messages[0].TimeCreated != items[0].StartedAtMs || c.Messages[1].TimeCreated != items[1].StartedAtMs {
		t.Fatalf("times = %d, %d", c.Messages[0].TimeCreated, c.Messages[1].TimeCreated)
	}
	first := decodeMap(t, c.Messages[1].Data)
	if first["providerID"] != "openai" || first["modelID"] != thread.Model || first["finish"] != "stop" {
		t.Fatalf("first reply = %v", first)
	}
	var tools []string
	for _, p := range partsOf(c, c.Messages[1].ID) {
		d := decodeMap(t, p.Data)
		if d["type"] != "tool" {
			continue
		}
		s := d["state"].(map[string]any)
		tools = append(tools, d["tool"].(string)+" "+str(s["status"])+" "+str(s["title"]))
		if d["tool"] == "apply_patch" && !strings.HasPrefix(s["input"].(map[string]any)["patchText"].(string), "*** Begin Patch\n*** Add File: /fixture/repo/hello.txt\n+hi\n") {
			t.Errorf("patch = %v", s["input"])
		}
	}
	if !slices.Equal(tools, []string{"bash completed ls", "apply_patch completed /fixture/repo/hello.txt"}) {
		t.Fatalf("tools = %q", tools)
	}
	second := decodeMap(t, c.Messages[3].Data)
	if second["error"].(map[string]any)["name"] != "MessageAbortedError" {
		t.Fatalf("interrupted reply = %v", second)
	}
	// Turn completion has whole-second resolution: it never moves a reply's
	// completion before its creation or its last item's completion.
	for _, m := range c.Messages {
		tm := decodeMap(t, m.Data)["time"].(map[string]any)
		if done, ok := tm["completed"].(float64); ok && int64(done) < m.TimeCreated {
			t.Errorf("%s completed %d before created %d", m.ID, int64(done), m.TimeCreated)
		}
	}
	if tm := first["time"].(map[string]any); tm["completed"] != float64(items[4].CompletedAtMs) {
		t.Errorf("first reply completed = %v, want item completion %d", tm["completed"], items[4].CompletedAtMs)
	}
}

func TestUnlistedTurnItems(t *testing.T) {
	c := &Converter{SessionID: "s"}
	c.AddHistory(&codexappserver.Thread{}, []codexappserver.Turn{{ID: "t1", Status: "completed"}}, []codexappserver.ItemEntry{
		{TurnID: "t1", Item: codexappserver.Item{Type: "userMessage", ID: "u1"}, StartedAtMs: 1},
		{TurnID: "t2", Item: codexappserver.Item{Type: "userMessage", ID: "u2"}, StartedAtMs: 5},
		{TurnID: "t2", Item: codexappserver.Item{Type: "agentMessage", ID: "a2", Text: "…"}, StartedAtMs: 6, CompletedAtMs: 7},
	})
	if len(c.Messages) != 3 || c.Messages[2].ID != "msg_a2" {
		t.Fatalf("messages = %+v", c.Messages)
	}
	if info := decodeMap(t, c.Messages[2].Data); info["finish"] != nil || info["error"] != nil {
		t.Fatalf("unlisted turn was settled: %v", info)
	}
}
