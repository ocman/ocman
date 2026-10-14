package claudeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// liveParts returns the last upsert of every streamed part, by part id.
func liveParts(got *seen) map[string]map[string]any {
	got.mu.Lock()
	defer got.mu.Unlock()
	parts := map[string]map[string]any{}
	for _, e := range got.events {
		if e.Type != "message.part.updated" {
			continue
		}
		var p struct {
			Part map[string]any `json:"part"`
		}
		_ = json.Unmarshal(e.Properties, &p)
		parts[p.Part["id"].(string)] = p.Part
	}
	return parts
}

// Forwarded subagent lines (parent_tool_use_id set) stream into the child
// session linked to the spawning Agent call, never into the parent (FR-3).
func TestLiveSubagentStaysUnderTask(t *testing.T) {
	h, got := promptHarness(t, claudetest.Fixture("subagent.jsonl"), func(h *liveHarness, e providerprotocol.Event) {
		if e.Type == "permission.asked" {
			var p platforms.LivePrompt
			_ = json.Unmarshal(e.Properties, &p)
			go func() {
				_ = h.adapter.RespondPermission(context.Background(), platforms.RespondPermissionRequest{SessionID: p["sessionID"].(string), PermissionID: p["id"].(string), Reply: "once"})
			}()
		}
	})
	id := h.create(t)
	h.turn(t, id, 1)
	const child = "agent-a000000000000000d"
	var task, childText, childBash map[string]any
	for _, p := range liveParts(got) {
		switch {
		case p["callID"] == "toolu_spike0010":
			task = p
		case p["sessionID"] == id && (p["callID"] == "toolu_spike0018" || p["text"] == "SUBAGENT_DONE"):
			t.Fatalf("subagent part leaked into the parent: %+v", p)
		case p["sessionID"] == child && p["text"] == "SUBAGENT_DONE":
			childText = p
		case p["sessionID"] == child && p["callID"] == "toolu_spike0018":
			childBash = p
		}
	}
	if task == nil || task["sessionID"] != id || task["tool"] != "task" {
		t.Fatalf("task part = %+v", task)
	}
	state := task["state"].(map[string]any)
	if state["metadata"].(map[string]any)["sessionId"] != child || state["status"] != "completed" {
		t.Fatalf("task state = %+v", state)
	}
	if childText == nil || childBash == nil || childBash["state"].(map[string]any)["status"] != "completed" {
		t.Fatalf("child parts: text=%+v bash=%+v", childText, childBash)
	}
	if len(h.live.snapshot(child)) == 0 {
		t.Fatal("child session has no live view")
	}
	// The child's events end with the turn: acknowledged and truncated
	// like the parent's, never replayed after a dashboard restart.
	waitFor(t, "child idle", func() bool { _, _, n := h.events.get(child); return n == 1 })
	for _, sid := range []string{id, child} {
		sum := sha256.Sum256([]byte(sid))
		spool := filepath.Join(h.dir, "spool", hex.EncodeToString(sum[:])+".jsonl")
		waitFor(t, "spool truncated after acknowledgment", func() bool { info, err := os.Stat(spool); return err == nil && info.Size() == 0 })
	}
}

// Token deltas upsert the same part ids the stored transcript converts to,
// and the final text agrees with the streamed one.
func TestPartialStreamAgreesWithStoredParts(t *testing.T) {
	rows := []string{
		`{"direction":"stdin","message":{"type":"user"}}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"msg_p","model":"haiku"}}}}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}}}`,
		`{"direction":"stdout","message":{"type":"assistant","parent_tool_use_id":null,"message":{"id":"msg_p","model":"haiku","content":[{"type":"thinking","thinking":"hmm","signature":"s"}]}}}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"SPIKE"}}}}`,
		`{"direction":"wait","message":"$HOLD"}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"_OK"}}}}`,
		`{"direction":"stdout","message":{"type":"assistant","parent_tool_use_id":null,"message":{"id":"msg_p","model":"haiku","stop_reason":"end_turn","content":[{"type":"text","text":"SPIKE_OK"}]}}}`,
		`{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false}}`,
	}
	hold := filepath.Join(t.TempDir(), "hold")
	fixturePath := filepath.Join(t.TempDir(), "partial.jsonl")
	if err := os.WriteFile(fixturePath, []byte(strings.ReplaceAll(strings.Join(rows, "\n")+"\n", "$HOLD", hold)), 0o600); err != nil {
		t.Fatal(err)
	}
	h, got := promptHarness(t, fixturePath, nil)
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first token", func() bool { return liveParts(got)["prt_msg_p_0001"]["text"] == "SPIKE" })
	if p := liveParts(got)["prt_msg_p_0000"]; p["type"] != "reasoning" || p["text"] != "hmm" {
		t.Fatalf("streamed thinking = %+v", p)
	}
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })

	// The stored transcript of the same message converts to the same parts.
	c := &Converter{SessionID: id}
	c.Add([]byte(`{"type":"assistant","timestamp":"2026-10-05T10:00:00Z","message":{"id":"msg_p","model":"haiku","content":[{"type":"thinking","thinking":"hmm","signature":"s"}]}}`))
	c.Add([]byte(`{"type":"assistant","timestamp":"2026-10-05T10:00:01Z","message":{"id":"msg_p","model":"haiku","stop_reason":"end_turn","content":[{"type":"text","text":"SPIKE_OK"}]}}`))
	c.Finish()
	live := liveParts(got)
	if len(c.Parts) != 2 {
		t.Fatalf("stored parts = %+v", c.Parts)
	}
	for _, stored := range c.Parts {
		var want map[string]any
		_ = json.Unmarshal(stored.Data, &want)
		if have := live[stored.ID]; have == nil || have["type"] != want["type"] || have["text"] != want["text"] {
			t.Fatalf("part %s: live %+v, stored %+v", stored.ID, have, want)
		}
	}
}

// liveCompleted returns the last upsert of each message's time.completed.
func liveCompleted(got *seen) map[string]any {
	got.mu.Lock()
	defer got.mu.Unlock()
	completed := map[string]any{}
	for _, e := range got.events {
		if e.Type != "message.updated" {
			continue
		}
		var m struct {
			Info struct {
				ID   string         `json:"id"`
				Time map[string]any `json:"time"`
			} `json:"info"`
		}
		_ = json.Unmarshal(e.Properties, &m)
		completed[m.Info.ID] = m.Info.Time["completed"]
	}
	return completed
}

// A live assistant message is published open, without time.completed, until
// the next message or the turn's result closes it: its lines carry no
// stop_reason, and a completed message without a finish reads as a settled
// turn, which hid the running turn's indicators.
func TestLiveMessageOpenUntilClosed(t *testing.T) {
	rows := []string{
		`{"direction":"stdin","message":{"type":"user"}}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"msg_a","model":"haiku"}}}}`,
		`{"direction":"stdout","message":{"type":"assistant","parent_tool_use_id":null,"message":{"id":"msg_a","model":"haiku","content":[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"file_path":"x"}}]}}}`,
		`{"direction":"stdout","message":{"type":"user","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_a","content":"ok"}]}}}`,
		`{"direction":"stdout","message":{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"msg_b","model":"haiku"}}}}`,
		`{"direction":"stdout","message":{"type":"assistant","parent_tool_use_id":null,"message":{"id":"msg_b","model":"haiku","content":[{"type":"text","text":"done"}]}}}`,
		`{"direction":"wait","message":"$HOLD"}`,
		`{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false}}`,
	}
	hold := filepath.Join(t.TempDir(), "hold")
	fixturePath := filepath.Join(t.TempDir(), "open.jsonl")
	if err := os.WriteFile(fixturePath, []byte(strings.ReplaceAll(strings.Join(rows, "\n")+"\n", "$HOLD", hold)), 0o600); err != nil {
		t.Fatal(err)
	}
	h, got := promptHarness(t, fixturePath, nil)
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second message text", func() bool { return liveParts(got)["prt_msg_b_0000"]["text"] == "done" })
	if c := liveCompleted(got); c["msg_a"] == nil || c["msg_b"] != nil {
		t.Fatalf("mid-turn completion: msg_a %v (want set), msg_b %v (want open)", c["msg_a"], c["msg_b"])
	}
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if c := liveCompleted(got); c["msg_b"] == nil {
		t.Fatal("result did not close the last message")
	}
}

// The directory catalog lists the commands the CLI's own initialize reports
// in that directory with ocman's plugin loaded; a skill a session's init
// named is offered as a skill.
func TestDirectoryCatalogFromCLI(t *testing.T) {
	commands := `{"subtype":"success","request_id":"ocman-initialize","response":{"commands":[` +
		`{"name":"review","description":"Review the diff","argumentHint":"[pr]"},` +
		`{"name":"ocman:ocman-sessions","description":"ocman sessions","argumentHint":""},"compact",` +
		`{"name":"ocman:ocman-routines","description":"ocman routines"},{"name":"user-skill"}],` +
		`"agents":[{"name":"reviewer","description":"Reviews code","model":"sonnet"},"explorer"]}}`
	// ocman's plugin and the user's own skills are skills before any
	// session in the directory reported its init line.
	plugin, err := WritePlugin(t.TempDir(), map[string][]byte{"ocman-routines": []byte("skill")}, "")
	if err != nil {
		t.Fatal(err)
	}
	config := fixture(t)
	if err := os.MkdirAll(filepath.Join(config, "skills", "user-skill"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "skills", "user-skill", "SKILL.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := serveLive(t, initFixture(t, commands), func(o *Options) { o.PluginDir, o.ConfigDir = plugin, config })
	cwds := filepath.Join(t.TempDir(), "cwd.log")
	t.Setenv("FAKE_CLAUDE_CWD_LOG", cwds)
	h.live.system(&proc{dir: h.dir}, []byte(`{"type":"system","subtype":"init","skills":["ocman:ocman-sessions"],"slash_commands":["review"]}`))
	cat, err := h.adapter.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: h.dir})
	if err != nil {
		t.Fatal(err)
	}
	want := []platforms.SlashCommandEntry{{Name: "review", Description: "Review the diff", Source: "command"},
		{Name: "ocman:ocman-sessions", Description: "ocman sessions", Source: "skill"}, {Name: "compact", Source: "command"},
		{Name: "ocman:ocman-routines", Description: "ocman routines", Source: "skill"}, {Name: "user-skill", Source: "skill"}}
	if len(cat.Models.Models) != 3 || len(cat.Commands) != len(want) {
		t.Fatalf("catalog = %+v", cat)
	}
	for i := range want {
		if cat.Commands[i] != want[i] {
			t.Fatalf("command %d = %+v, want %+v", i, cat.Commands[i], want[i])
		}
	}
	if len(cat.Agents) != 2 || cat.Agents[0] != (platforms.AgentCatalogEntry{Name: "reviewer", Description: "Reviews code", Model: "sonnet", Mode: "subagent", Kind: "subagent"}) ||
		cat.Agents[1].Name != "explorer" || cat.Agents[1].Mode != "subagent" {
		t.Fatalf("agents = %+v", cat.Agents)
	}
	if args := h.launchArgs(t); !strings.Contains(strings.Join(args, " "), "--plugin-dir "+plugin) || strings.Contains(strings.Join(args, " "), "--session-id") {
		t.Fatalf("initialize args = %q", args)
	}
	if _, err := h.adapter.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: h.dir}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(must(os.ReadFile(cwds)))); got != h.dir && !strings.HasSuffix(got, h.dir) || strings.Count(got, "\n") != 0 {
		t.Fatalf("initialize ran in %q; want once in %s (cached)", got, h.dir)
	}

	// A CLI that cannot answer leaves commands empty; models stay.
	t.Setenv("FAKE_CLAUDE_FIXTURE", claudetest.Fixture("plain.jsonl"))
	cat, err = h.adapter.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: t.TempDir()})
	if err != nil || len(cat.Commands) != 0 || len(cat.Models.Models) != 3 {
		t.Fatalf("fallback catalog = %+v %v", cat, err)
	}
}

// A child that dies during its first turn has already started the session's
// transcript (the CLI echoed the input), so the respawn resumes it instead of
// reusing --session-id for a transcript that exists.
func TestFirstTurnCrashResumes(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "crash.jsonl")
	rows := `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:1","parent_tool_use_id":null,"message":{"role":"user","content":"go"}}}
{"direction":"exit","message":1}
`
	if err := os.WriteFile(fixturePath, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	h := startLive(t, fixturePath, nil)
	id := h.create(t)
	for i := 1; i <= 2; i++ {
		if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "crashed turn idle", func() bool { _, _, n := h.events.get(id); return n == i })
	}
	if args := strings.Join(h.launchArgs(t), " "); !strings.Contains(args, "--resume "+id) || strings.Contains(args, "--session-id") {
		t.Fatalf("respawn args = %q", args)
	}
}

// A rename during a running turn shows at once but is written to the
// transcript only after the turn, never interleaved with the CLI's writes.
func TestRenameDuringTurnWaitsForIdle(t *testing.T) {
	hold := filepath.Join(t.TempDir(), "hold")
	t.Setenv("FAKE_CLAUDE_HOLD", hold)
	h := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running turn", func() bool { h.live.mu.Lock(); defer h.live.mu.Unlock(); return h.live.procs[id] != nil })
	path := filepath.Join(h.live.opts.ConfigDir, "projects", "-live", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + h.dir + `","sessionId":"` + id + `","uuid":"u1","timestamp":"2026-10-05T10:00:00Z","message":{"role":"user","content":"first prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.live.idx.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.adapter.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: id, Title: "Renamed"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(must(os.ReadFile(path))), "custom-title") {
		t.Fatal("rename wrote the transcript while the CLI was writing it")
	}
	if s, err := h.adapter.Sessions(t.Context(), h.dir, 0); err != nil || len(s) != 1 || s[0].Title != "Renamed" {
		t.Fatalf("sessions = %+v %v", s, err)
	}
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	waitFor(t, "title written", func() bool { return strings.Contains(string(must(os.ReadFile(path))), `"customTitle":"Renamed"`) })
	if o, _ := h.live.ownedSession(id); o.TitlePending {
		t.Fatal("title still pending after it was written")
	}
}
