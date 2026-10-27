// Package codexprovider implements the Codex backend: it drives
// `codex app-server` and converts its threads into ocman's event format.
package codexprovider

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// PlatformID is the platform id the provider advertises in /hello.
const PlatformID = "codex"

// Converter is the single Codex -> OpenCode v1 message/part converter. Stored
// history (thread/turns/list, thread/items/list) and live item notifications
// carry the same ThreadItem shape and go through it, so ids agree:
//
//   - a user message is msg_<userMessage item id>, its first part
//     prt_<item id> and later parts prt_<item id>_<input index>;
//   - the assistant reply that follows is msg_<first reply item id>, and each
//     reply item is one part, prt_<item id>;
//   - a failed turn without reply items gets a part-less reply, msg_<turn id>,
//     carrying its error.
type Converter struct {
	SessionID  string
	ProviderID string // model provider, such as "openai"
	ModelID    string
	Messages   []db.Message
	Parts      []db.Part
	// Usage is per-turn token usage (ReadRolloutUsage), applied to each
	// turn's reply when it settles.
	Usage map[string]TurnUsage
	cur   *group
	last  int64 // creation time of the newest message
}

type group struct {
	info     map[string]any
	parts    []map[string]any
	times    []int64
	sections map[string]int // streamed reasoning summary index per part
}

// AddHistory converts a paged read: the thread's metadata, its turns without
// items, and its stored items in order. Stored items carry their timestamps
// on the entry, which become the item's own, as on live notifications. Items
// of a turn missing from turns (one that started after the turn listing) are
// converted after the listed turns, still unsettled.
func (c *Converter) AddHistory(t *codexappserver.Thread, turns []codexappserver.Turn, entries []codexappserver.ItemEntry) {
	c.defaults(t)
	byTurn := map[string][]codexappserver.Item{}
	var unlisted []string
	listed := map[string]bool{}
	for _, turn := range turns {
		listed[turn.ID] = true
	}
	for _, e := range entries {
		it := e.Item
		if it.StartedAtMs == 0 {
			it.StartedAtMs = e.StartedAtMs
		}
		if it.CompletedAtMs == 0 {
			it.CompletedAtMs = e.CompletedAtMs
		}
		if !listed[e.TurnID] && byTurn[e.TurnID] == nil {
			unlisted = append(unlisted, e.TurnID)
		}
		byTurn[e.TurnID] = append(byTurn[e.TurnID], it)
	}
	for _, turn := range turns {
		turn.Items = byTurn[turn.ID]
		// A separate app-server marks unloaded in-progress turns interrupted.
		// Without a terminal timestamp or error this is not proof of an abort.
		// Leave the reply open; only live observation can say it is running.
		if turn.Status == codexappserver.TurnInterrupted && turn.CompletedAt == 0 && turn.Error == nil {
			turn.Status = codexappserver.TurnInProgress
		}
		c.AddTurn(&turn)
	}
	for _, id := range unlisted {
		c.AddTurn(&codexappserver.Turn{ID: id, Status: codexappserver.TurnInProgress, Items: byTurn[id]})
	}
}

func (c *Converter) defaults(t *codexappserver.Thread) {
	if c.SessionID == "" {
		c.SessionID = t.ID
	}
	if c.ProviderID == "" {
		c.ProviderID = t.ModelProvider
	}
	if c.ModelID == "" {
		c.ModelID = t.Model
	}
}

// AddTurn converts one turn's items and settles the open reply with the
// turn's outcome.
func (c *Converter) AddTurn(turn *codexappserver.Turn) {
	start := turn.StartedAt * 1000
	for _, it := range turn.Items {
		c.AddItem(it, start)
	}
	c.Settle(turn)
	if turn.Status == codexappserver.TurnInProgress && c.cur != nil {
		c.cur.info = openInfo(c.cur.info)
	}
	c.Finish()
}

// AddItem converts one item. fallback is used when the item carries no
// timestamps (older servers).
func (c *Converter) AddItem(it codexappserver.Item, fallback int64) {
	ts := itemTime(it, fallback)
	if it.Type == codexappserver.ItemUserMessage {
		c.Finish()
		c.start("msg_"+it.ID, "user", ts)
		for i, in := range it.Content {
			if p := userPart(in); p != nil {
				c.part(p, partID(it.ID, i), ts)
			}
		}
		return
	}
	part := ItemPart(it, ts)
	if part == nil {
		return
	}
	if c.cur == nil || c.cur.info["role"] != "assistant" {
		c.Finish()
		c.startAssistant("msg_"+it.ID, ts)
	}
	c.part(part, "prt_"+it.ID, ts)
	if end := it.CompletedAtMs; end > 0 {
		setCompleted(c.cur.info, end)
	}
}

func itemTime(it codexappserver.Item, fallback int64) int64 {
	if it.StartedAtMs > 0 {
		return it.StartedAtMs
	}
	if it.CompletedAtMs > 0 {
		return it.CompletedAtMs
	}
	return fallback
}

func partID(item string, index int) string {
	if index == 0 {
		return "prt_" + item
	}
	return "prt_" + item + "_" + strconv.Itoa(index)
}

func (c *Converter) start(id, role string, ts int64) {
	c.cur = &group{info: map[string]any{"id": id, "sessionID": c.SessionID, "role": role, "time": map[string]any{"created": ts}}}
}

func (c *Converter) startAssistant(id string, ts int64) {
	c.start(id, "assistant", ts)
	info := c.cur.info
	info["providerID"], info["modelID"] = c.ProviderID, c.ModelID
	info["mode"], info["agent"], info["cost"] = "", "", 0.0
	info["tokens"] = map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}}
}

func (c *Converter) part(fields map[string]any, id string, ts int64) {
	g := c.cur
	fields["id"], fields["messageID"], fields["sessionID"] = id, g.info["id"], c.SessionID
	for i, p := range g.parts {
		if p["id"] == id { // a completed item replaces its started form
			g.parts[i], g.times[i] = fields, ts
			return
		}
	}
	g.parts = append(g.parts, fields)
	g.times = append(g.times, ts)
}

// Finish flushes the open message. Call it after the last item.
func (c *Converter) Finish() {
	g := c.cur
	if g == nil {
		return
	}
	c.cur = nil
	id := g.info["id"].(string)
	created := g.info["time"].(map[string]any)["created"].(int64)
	c.last = max(c.last, created)
	data, _ := json.Marshal(g.info)
	c.Messages = append(c.Messages, db.Message{ID: id, SessionID: c.SessionID, TimeCreated: created, Data: data})
	for i, p := range g.parts {
		data, _ := json.Marshal(p)
		c.Parts = append(c.Parts, db.Part{ID: p["id"].(string), MessageID: id, SessionID: c.SessionID, TimeCreated: g.times[i], Data: data})
	}
}

func setCompleted(info map[string]any, ts int64) {
	info["time"].(map[string]any)["completed"] = ts
}

// settle records how the turn ended on its reply message. The turn's
// completion has whole-second resolution, so it only ever moves the reply's
// completion later: never before its creation or an item's completion.
func settle(info map[string]any, turn *codexappserver.Turn) {
	if turn.CompletedAt > 0 {
		tm := info["time"].(map[string]any)
		end := max(turn.CompletedAt*1000, tm["created"].(int64))
		if prev, ok := tm["completed"].(int64); ok {
			end = max(end, prev)
		}
		setCompleted(info, end)
	}
	switch turn.Status {
	case codexappserver.TurnCompleted:
		info["finish"] = "stop"
	case codexappserver.TurnInterrupted:
		info["error"] = map[string]any{"name": "MessageAbortedError", "data": map[string]any{"message": "Interrupted"}}
	case codexappserver.TurnFailed:
		msg := "Turn failed"
		if turn.Error != nil && turn.Error.Message != "" {
			msg = turn.Error.Message
		}
		info["error"] = map[string]any{"name": "APIError", "data": map[string]any{"message": msg}}
	}
}

// userPart converts one user input. Unsupported inputs are dropped.
func userPart(in codexappserver.UserInput) map[string]any {
	switch in.Type {
	case "text":
		return map[string]any{"type": "text", "text": in.Text}
	case "image":
		return map[string]any{"type": "file", "mime": dataURLMime(in.URL), "url": in.URL}
	case "localImage":
		return map[string]any{"type": "file", "mime": imageMime(in.Path), "url": "file://" + in.Path, "filename": baseName(in.Path)}
	}
	return nil
}

func dataURLMime(url string) string {
	if rest, ok := strings.CutPrefix(url, "data:"); ok {
		if mime, _, ok := strings.Cut(rest, ";"); ok && mime != "" {
			return mime
		}
	}
	return "image/png"
}

func imageMime(path string) string {
	switch strings.ToLower(path[strings.LastIndex(path, ".")+1:]) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	}
	return "image/png"
}

func baseName(path string) string { return path[strings.LastIndex(path, "/")+1:] }
