// Package acptest supplies a disposable ACP peer and its native history for
// tests. It never invokes a real coding agent or opens a platform database.
package acptest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

type State struct {
	CLI       string                     `json:"cli"`
	Home      string                     `json:"home"`
	Directory string                     `json:"directory"`
	CreatedAt int64                      `json:"createdAt"`
	Revision  int64                      `json:"revision"`
	Turns     []codexappserver.Turn      `json:"turns"`
	Items     []codexappserver.ItemEntry `json:"items"`
	Calls     []string                   `json:"calls"`
}

func ReadState(path string) State {
	var state State
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &state)
	return state
}

type Config struct {
	State      string
	Permission bool
	Hang       bool
	Exit       bool
	Question   bool
	Reply      string
}

type agent struct {
	config Config
	conn   *acp.Connection
	mu     sync.Mutex
	state  State
	cancel context.CancelFunc
}

func Serve(r io.Reader, w io.Writer, config Config) {
	a := &agent{config: config, state: ReadState(config.State)}
	ready := make(chan struct{})
	a.conn = acp.NewConnection(func(ctx context.Context, method string, raw json.RawMessage) (any, *acp.RequestError) {
		<-ready
		return a.handle(ctx, method, raw)
	}, w, r)
	close(ready)
	<-a.conn.Done()
}

func (a *agent) saveLocked() {
	a.state.Revision++
	data, err := json.Marshal(a.state)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(a.config.State+".tmp", data, 0o600); err != nil {
		panic(err)
	}
	if err := os.Rename(a.config.State+".tmp", a.config.State); err != nil {
		panic(err)
	}
}

func (a *agent) handle(ctx context.Context, method string, raw json.RawMessage) (any, *acp.RequestError) {
	a.mu.Lock()
	a.state.Calls = append(a.state.Calls, method)
	a.saveLocked()
	a.mu.Unlock()
	switch method {
	case "initialize":
		a.mu.Lock()
		a.state.CLI, a.state.Home = os.Getenv("CODEX_PATH"), os.Getenv("CODEX_HOME")
		a.saveLocked()
		a.mu.Unlock()
		return map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "codex-acp-test", "version": "2.5.0"},
			"agentCapabilities": map[string]any{"loadSession": true, "sessionCapabilities": map[string]any{"resume": map[string]any{}, "fork": map[string]any{}}, "mcpCapabilities": map[string]any{"http": true}, "promptCapabilities": map[string]any{"image": true}},
			"authMethods":       []any{}, "_meta": map[string]any{"steering": map[string]any{"supported": true}}}, nil
	case "session/new":
		var request struct {
			Cwd string `json:"cwd"`
		}
		_ = json.Unmarshal(raw, &request)
		a.mu.Lock()
		a.state.Directory = request.Cwd
		a.state.CreatedAt = time.Now().Unix()
		a.saveLocked()
		a.mu.Unlock()
		if path := os.Getenv("FAKE_CODEX_LAUNCHES"); path != "" {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				panic(err)
			}
			_, _ = fmt.Fprintf(f, "%s ACP %s\n", os.Getenv("CODEX_HOME"), raw)
			_ = f.Close()
		}
		return map[string]any{"sessionId": "thr"}, nil
	case "session/resume", "session/load", "session/set_config_option", "authenticate":
		if os.Getenv("FAKE_CODEX_OWNERSHIP_CONFLICT") != "" && method == "session/resume" {
			return nil, &acp.RequestError{Code: -32600, Message: "thread thr already has an active writer"}
		}
		return map[string]any{}, nil
	case "session/fork":
		return map[string]any{"sessionId": "thr-fork"}, nil
	case "_session/steering":
		return map[string]any{"outcome": "injected"}, nil
	case "session/cancel":
		a.mu.Lock()
		if a.cancel != nil {
			a.cancel()
		}
		a.mu.Unlock()
		return nil, nil
	case "session/prompt":
		var req acp.PromptRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, acp.NewInvalidParams(nil)
		}
		if a.config.Exit {
			os.Exit(3)
		}
		return a.prompt(ctx, req)
	default:
		return nil, acp.NewMethodNotFound(method)
	}
}

func (a *agent) prompt(ctx context.Context, req acp.PromptRequest) (any, *acp.RequestError) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.mu.Lock()
	a.cancel = cancel
	index := len(a.state.Turns) + 1
	id, user, reasoning, reply := fmt.Sprintf("t%d", index), fmt.Sprintf("u%d", index), fmt.Sprintf("r%d", index), fmt.Sprintf("a%d", index)
	ts := time.Now().UnixMilli()
	turn := codexappserver.Turn{ID: id, Status: codexappserver.TurnInProgress, StartedAt: ts / 1000}
	items := []codexappserver.Item{
		{ID: user, Type: "userMessage", Content: []codexappserver.UserInput{codexappserver.TextInput("hello")}, StartedAtMs: ts},
		{ID: reasoning, Type: "reasoning", Summary: []string{""}, StartedAtMs: ts},
		{ID: reply, Type: "agentMessage", Text: "", StartedAtMs: ts},
	}
	for _, it := range items {
		a.state.Items = append(a.state.Items, codexappserver.ItemEntry{TurnID: id, Item: it, StartedAtMs: ts})
	}
	a.state.Turns = append(a.state.Turns, turn)
	a.saveLocked()
	a.mu.Unlock()
	update := func(text string) {
		u := acp.UpdateAgentMessageText(text)
		u.AgentMessageChunk.MessageId = &reply
		_ = a.conn.SendNotification(ctx, "session/update", acp.SessionNotification{SessionId: req.SessionId, Update: u})
	}
	message := a.config.Reply
	if message == "" {
		message = "Hello from ACP"
	}
	middle := len(message) / 2
	update(message[:middle])
	stop := "end_turn"
	if a.config.Permission {
		kind := acp.ToolKindExecute
		result, err := acp.SendRequest[acp.RequestPermissionResponse](a.conn, ctx, "session/request_permission", acp.RequestPermissionRequest{
			SessionId: req.SessionId, ToolCall: acp.ToolCallUpdate{ToolCallId: "command", Kind: &kind, RawInput: map[string]any{"command": "ls"}},
			Options: []acp.PermissionOption{{OptionId: "allow", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow"}, {OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject"}},
		})
		if err != nil || result.Outcome.Cancelled != nil || result.Outcome.Selected.OptionId == "reject" {
			stop = "cancelled"
		}
	}
	if a.config.Hang {
		<-ctx.Done()
		stop = "cancelled"
	}
	if a.config.Question {
		result, err := acp.SendRequest[acp.UnstableCreateElicitationResponse](a.conn, ctx, "elicitation/create", map[string]any{
			"mode": "form", "sessionId": req.SessionId, "toolCallId": "question-tool", "message": "Pick one",
			"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"choice": map[string]any{
				"type": "string", "title": "Which choice?", "oneOf": []any{map[string]any{"const": "a", "title": "A"}, map[string]any{"const": "b", "title": "B"}},
			}}},
		})
		if err != nil || result.Accept == nil {
			stop = "cancelled"
		}
	}
	if stop == "end_turn" {
		update(message[middle:])
	}
	a.mu.Lock()
	last := &a.state.Turns[len(a.state.Turns)-1]
	last.Status, last.CompletedAt = codexappserver.TurnCompleted, time.Now().Unix()
	if stop == "cancelled" {
		last.Status = codexappserver.TurnInterrupted
	}
	entry := &a.state.Items[len(a.state.Items)-1]
	entry.Item.Text = message
	if stop != "end_turn" {
		entry.Item.Text = message[:middle]
	}
	entry.Item.CompletedAtMs = time.Now().UnixMilli()
	a.saveLocked()
	a.mu.Unlock()
	return map[string]any{"stopReason": stop}, nil
}
