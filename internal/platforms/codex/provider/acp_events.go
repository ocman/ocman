package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	log "github.com/sirupsen/logrus"
)

type acpItem struct {
	native codexappserver.Item
	tool   acp.SessionUpdateToolCall
}

func (a *acpExecution) update(ctx context.Context, p acp.SessionNotification) error {
	id := string(p.SessionId)
	a.mu.Lock()
	turn := a.turns[id]
	a.mu.Unlock()
	if turn == nil {
		return nil // catalog/config updates outside an admitted turn
	}
	turn.mu.Lock()
	defer turn.mu.Unlock()
	u := p.Update
	var itemID string
	switch {
	case u.AgentMessageChunk != nil:
		itemID = a.chunk(turn, u.AgentMessageChunk.Content, u.AgentMessageChunk.MessageId, false)
	case u.AgentThoughtChunk != nil:
		itemID = a.chunk(turn, u.AgentThoughtChunk.Content, u.AgentThoughtChunk.MessageId, true)
	case u.ToolCall != nil:
		itemID = string(u.ToolCall.ToolCallId)
		turn.items[itemID] = &acpItem{tool: *u.ToolCall}
		turn.items[itemID].native = acpToolItem(*u.ToolCall, turn.started)
	case u.ToolCallUpdate != nil:
		itemID = string(u.ToolCallUpdate.ToolCallId)
		entry := turn.items[itemID]
		if entry == nil {
			return fmt.Errorf("codex ACP updated unknown tool %q", itemID)
		}
		mergeACPTool(&entry.tool, u.ToolCallUpdate)
		entry.native = acpToolItem(entry.tool, turn.started)
	default:
		return nil
	}
	if itemID == "" {
		return nil
	}
	if !turn.userSeeded && !turn.command {
		if err := a.seedUser(ctx, id, itemID, turn); err != nil {
			return err
		}
	}
	entry := turn.items[itemID]
	turn.conv.AddItem(entry.native, turn.started)
	info, parts := turn.conv.Live(itemID)
	a.b.publishMessage(id, info, parts)
	a.b.publicationMu.Lock()
	pending := len(a.b.pending[id]) != 0
	a.b.publicationMu.Unlock()
	if pending {
		return errors.New("codex ACP event could not be persisted")
	}
	return nil
}

func (a *acpExecution) chunk(turn *acpTurn, content acp.ContentBlock, messageID *string, thought bool) string {
	if content.Text == nil {
		return ""
	}
	if messageID == nil || *messageID == "" {
		// The adapter also emits notices without a native message identity.
		// Keep them in diagnostics, not as unconfirmable conversation messages.
		log.WithField("notice", content.Text.Text).Warn("Codex ACP notice")
		return ""
	}
	id := *messageID
	entry := turn.items[id]
	if entry == nil {
		kind := codexappserver.ItemAgentMessage
		if thought {
			kind = codexappserver.ItemReasoning
		}
		entry = &acpItem{native: codexappserver.Item{ID: id, Type: kind, StartedAtMs: turn.started}}
		turn.items[id] = entry
	}
	if thought {
		if len(entry.native.Summary) == 0 {
			entry.native.Summary = []string{""}
		}
		entry.native.Summary[0] += content.Text.Text
	} else {
		entry.native.Text += content.Text.Text
	}
	return id
}

func mergeACPTool(tool *acp.SessionUpdateToolCall, update *acp.SessionToolCallUpdate) {
	if update.Title != nil {
		tool.Title = *update.Title
	}
	if update.Kind != nil {
		tool.Kind = *update.Kind
	}
	if update.Status != nil {
		tool.Status = *update.Status
	}
	if update.RawInput != nil {
		tool.RawInput = update.RawInput
	}
	if update.RawOutput != nil {
		tool.RawOutput = update.RawOutput
	}
	if update.Content != nil {
		tool.Content = update.Content
	}
	if update.Locations != nil {
		tool.Locations = update.Locations
	}
}

func acpToolItem(tool acp.SessionUpdateToolCall, started int64) codexappserver.Item {
	it := codexappserver.Item{ID: string(tool.ToolCallId), Type: codexappserver.ItemDynamicToolCall,
		Tool: tool.Title, Status: "inProgress", StartedAtMs: started}
	it.Arguments, _ = json.Marshal(tool.RawInput)
	input := arguments(it.Arguments)
	if command, ok := input["command"].(string); ok {
		it.Type, it.Command = codexappserver.ItemCommandExecution, command
	}
	var output []string
	for _, content := range tool.Content {
		if content.Content != nil && content.Content.Content.Text != nil {
			output = append(output, content.Content.Content.Text.Text)
		}
	}
	text := strings.Join(output, "\n")
	if raw, ok := tool.RawOutput.(string); ok {
		text = raw
	}
	it.AggregatedOutput = &text
	it.Result, _ = json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})
	switch tool.Status {
	case acp.ToolCallStatusCompleted:
		it.Status, it.CompletedAtMs = "completed", now()
	case acp.ToolCallStatusFailed:
		it.Status, it.CompletedAtMs = "failed", now()
	}
	return it
}

func (a *acpExecution) seedUser(ctx context.Context, id, firstItem string, turn *acpTurn) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	native, _, err := a.b.conn(ctx)
	if err != nil {
		return err
	}
	for {
		entries, err := native.ListItems(ctx, id)
		if err != nil && !acpHistoryPending(err) {
			return err
		}
		index := -1
		for i, entry := range entries {
			if entry.Item.Type == codexappserver.ItemUserMessage {
				index = i
			}
		}
		if index >= 0 && entries[index].Item.ID != turn.baseUser {
			turn.nativeID = entries[index].TurnID
			user := entries[index].Item
			if user.StartedAtMs == 0 {
				user.StartedAtMs = entries[index].StartedAtMs
			}
			turn.conv.AddItem(user, turn.started)
			info, parts := turn.conv.Live(user.ID)
			a.b.publishMessage(id, info, parts)
			// Native conversion groups the reply under its first item, even an
			// empty reasoning item ACP never reports. Seed preceding items so
			// streamed and historical assistant message IDs stay identical.
			for _, entry := range entries[index+1:] {
				if entry.Item.ID == firstItem {
					break
				}
				item := entry.Item
				if item.StartedAtMs == 0 {
					item.StartedAtMs = entry.StartedAtMs
				}
				if item.CompletedAtMs == 0 {
					item.CompletedAtMs = entry.CompletedAtMs
				}
				turn.conv.AddItem(item, turn.started)
				info, parts = turn.conv.Live(entry.Item.ID)
				a.b.publishMessage(id, info, parts)
			}
			turn.userSeeded = true
			return nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("codex ACP user identity unavailable: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// reconcile publishes the authoritative native turn before idle, including its
// original item IDs, full tool payloads and rollout-derived token accounting.
func (a *acpExecution) reconcile(parent context.Context, id string, turn *acpTurn, cancelled bool) error {
	ctx, cancel := context.WithTimeout(parent, rpcTimeout)
	defer cancel()
	native, _, err := a.b.conn(ctx)
	if err != nil {
		return err
	}
	last, err := acpFinalTurn(ctx, native, id, turn, cancelled)
	if err != nil {
		return err
	}
	if last == nil {
		return nil
	}
	if turn.nativeID != "" && last.ID != turn.nativeID {
		return errors.New("codex ACP native turn identity changed before settlement")
	}
	entries, err := native.ListItems(ctx, id)
	if err != nil {
		return err
	}
	last.Items = nil
	for _, entry := range entries {
		if entry.TurnID == last.ID {
			it := entry.Item
			if it.StartedAtMs == 0 {
				it.StartedAtMs = entry.StartedAtMs
			}
			if it.CompletedAtMs == 0 {
				it.CompletedAtMs = entry.CompletedAtMs
			}
			last.Items = append(last.Items, it)
		}
	}
	conv := &Converter{SessionID: id, ProviderID: turn.conv.ProviderID, ModelID: turn.conv.ModelID}
	if thread, err := native.ReadThread(ctx, id, false); err == nil {
		conv.defaults(thread)
		conv.Usage, _ = ReadRolloutUsage(thread.Path)
	}
	if turn.title != "" {
		if err := native.SetThreadName(ctx, id, turn.title); err != nil {
			return fmt.Errorf("persisting Codex session title: %w", err)
		}
	}
	conv.AddTurn(last)
	for _, message := range conv.Messages {
		var info map[string]any
		if err := json.Unmarshal(message.Data, &info); err != nil {
			return err
		}
		a.b.publishMessage(id, info, nil)
	}
	for _, part := range conv.Parts {
		var fields map[string]any
		if err := json.Unmarshal(part.Data, &fields); err != nil {
			return err
		}
		a.b.publishMessage(id, nil, []map[string]any{fields})
	}
	if last.Status == codexappserver.TurnFailed {
		turn.failed = true
	}
	turn.cancelled = last.Status == codexappserver.TurnInterrupted
	return nil
}

// Readers can lag an ACP completion or infer "interrupted" merely because
// their own app-server never loaded the active writer. Wait for native terminal
// proof; retrying these reads never replays a mutation.
func acpFinalTurn(ctx context.Context, native *codexappserver.Client, id string, turn *acpTurn, cancelled bool) (*codexappserver.Turn, error) {
	for {
		turns, err := native.ListTurns(ctx, id, "notLoaded")
		if err != nil && !acpHistoryPending(err) {
			return nil, err
		}
		if len(turns) == 0 {
			if cancelled || turn.command {
				return nil, nil
			}
		} else {
			last := turns[len(turns)-1]
			if last.ID == turn.baseTurn {
				if cancelled || turn.command {
					return nil, nil
				}
			} else if last.Status != codexappserver.TurnInProgress &&
				(last.Status != codexappserver.TurnInterrupted || last.CompletedAt > 0 || last.Error != nil || cancelled) {
				return &last, nil
			}
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("waiting for Codex native settlement: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func acpHistoryPending(err error) bool {
	var rpc *codexappserver.RPCError
	return errors.As(err, &rpc) && rpc.Code == -32600 &&
		(strings.Contains(rpc.Message, "not materialized") || strings.Contains(rpc.Message, "no rollout") ||
			strings.Contains(rpc.Message, "thread not loaded") || strings.Contains(rpc.Message, "invalid thread id"))
}
