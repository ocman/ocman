package codexprovider

import (
	"maps"
	"strings"

	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// Live returns the open message and the parts of item itemID, for publishing
// upserts after AddItem. A user message's parts are all its inputs.
func (c *Converter) Live(itemID string) (info map[string]any, parts []map[string]any) {
	if c.cur == nil {
		return nil, nil
	}
	prefix := "prt_" + itemID
	for _, p := range c.cur.parts {
		if id := p["id"].(string); id == prefix || strings.HasPrefix(id, prefix+"_") {
			parts = append(parts, p)
		}
	}
	return openInfo(c.cur.info), parts
}

// Delta appends streamed text to an open item's part and returns it: agent
// message and reasoning text, or command output. Unknown items return nil.
func (c *Converter) Delta(method string, d codexappserver.DeltaNotification) map[string]any {
	if c.cur == nil {
		return nil
	}
	id := "prt_" + d.ItemID
	var part map[string]any
	for _, p := range c.cur.parts {
		if p["id"] == id {
			part = p
		}
	}
	if part == nil {
		return nil
	}
	switch method {
	case codexappserver.NotifyAgentMessageDelta, codexappserver.NotifyReasoningSummary, codexappserver.NotifyReasoningText:
		if part["type"] != "text" && part["type"] != "reasoning" {
			return nil
		}
		text, _ := part["text"].(string)
		// A new reasoning summary section starts a new paragraph.
		if method == codexappserver.NotifyReasoningSummary && d.SummaryIndex != nil && *d.SummaryIndex > 0 && c.section(id) != *d.SummaryIndex {
			c.setSection(id, *d.SummaryIndex)
			text += "\n\n"
		}
		part["text"] = text + d.Delta
	case codexappserver.NotifyCommandOutputDelta:
		state, ok := part["state"].(map[string]any)
		if !ok {
			return nil
		}
		output, _ := state["output"].(string)
		state["output"] = output + d.Delta
		state["metadata"].(map[string]any)["output"] = state["output"]
	default:
		return nil
	}
	return part
}

func (c *Converter) section(part string) int {
	return c.cur.sections[part]
}

func (c *Converter) setSection(part string, n int) {
	if c.cur.sections == nil {
		c.cur.sections = map[string]int{}
	}
	c.cur.sections[part] = n
}

// Settle records the turn's outcome on the open reply and returns its final
// info. A failed turn without a reply gets one, msg_<turn id>, carrying the
// error; any other turn without a reply returns nil. Stored history (AddTurn)
// and live turn ends both settle here, so the ids agree.
func (c *Converter) Settle(turn *codexappserver.Turn) map[string]any {
	if c.cur == nil || c.cur.info["role"] != "assistant" {
		if turn.Status != codexappserver.TurnFailed {
			return nil
		}
		c.Finish()
		ts := max(turn.StartedAt*1000, turn.CompletedAt*1000, c.last)
		c.startAssistant("msg_"+turn.ID, ts)
		setCompleted(c.cur.info, ts)
	}
	settle(c.cur.info, turn)
	if u, ok := c.Usage[turn.ID]; ok {
		applyUsage(c.cur.info, u)
	}
	return c.cur.info
}

// SetUsage records a turn's token usage on the open reply and returns its
// info; nil when no reply is open yet.
func (c *Converter) SetUsage(u TurnUsage) map[string]any {
	if c.cur == nil || c.cur.info["role"] != "assistant" {
		return nil
	}
	applyUsage(c.cur.info, u)
	return openInfo(c.cur.info)
}

// Tokens maps Codex usage to v1 tokens. Codex counts cached input inside
// input and reasoning inside output; v1 keeps them apart.
func Tokens(u codexappserver.TokenUsage) map[string]any {
	return map[string]any{"input": u.InputTokens - u.CachedInputTokens, "output": u.OutputTokens - u.ReasoningOutputTokens,
		"reasoning": u.ReasoningOutputTokens, "cache": map[string]any{"read": u.CachedInputTokens, "write": u.CacheWriteInputTokens}}
}

// Sub subtracts base from u, the thread totals at a turn's start, giving
// the turn's own usage.
func Sub(u, base codexappserver.TokenUsage) codexappserver.TokenUsage {
	return codexappserver.TokenUsage{TotalTokens: u.TotalTokens - base.TotalTokens, InputTokens: u.InputTokens - base.InputTokens,
		CachedInputTokens: u.CachedInputTokens - base.CachedInputTokens, CacheWriteInputTokens: u.CacheWriteInputTokens - base.CacheWriteInputTokens,
		OutputTokens: u.OutputTokens - base.OutputTokens, ReasoningOutputTokens: u.ReasoningOutputTokens - base.ReasoningOutputTokens}
}

// openInfo is an assistant message as published while its turn runs: the
// converter stamps completion per item, but the UI reads a completed message
// as a settled turn, so the completion is withheld until Settle.
func openInfo(info map[string]any) map[string]any {
	t, ok := info["time"].(map[string]any)
	if info["role"] != "assistant" || !ok || t["completed"] == nil {
		return info
	}
	out := maps.Clone(info)
	out["time"] = map[string]any{"created": t["created"]}
	return out
}
