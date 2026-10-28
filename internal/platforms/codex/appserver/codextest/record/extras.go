//go:build codexrecord

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// extras records the operations beyond plain turns: one turn for history and
// usage, then a user shell command, compaction, an inline review, forks, the
// skills list and account reads. Each step waits for its turn/completed, if
// any, and the stored history and rollout file are saved afterwards so tests
// see how each operation is stored.
func extras(ctx context.Context, c *codexappserver.Client, h *handler, id, home, out string, rec *recorder) error {
	step := func(name string) { fmt.Fprintln(os.Stderr, "step", name) }
	// waitTurn reports whether a turn completed within d.
	waitTurn := func(d time.Duration) (*codexappserver.Turn, bool) {
		select {
		case n := <-h.turns:
			fmt.Fprintln(os.Stderr, "  turn", n.Turn.ID, n.Turn.Status)
			return &n.Turn, true
		case <-time.After(d):
			fmt.Fprintln(os.Stderr, "  no turn/completed within", d)
			return nil, false
		}
	}
	call := func(method string, params any) (json.RawMessage, error) {
		var raw json.RawMessage
		err := c.Call(ctx, method, params, &raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "  ", method, "error:", err)
		}
		return raw, err
	}

	step("turn")
	if _, err := c.StartTurn(ctx, codexappserver.TurnStartParams{ThreadID: id, Input: []codexappserver.UserInput{codexappserver.TextInput("Reply with the word ready.")}}); err != nil {
		return err
	}
	first, ok := waitTurn(3 * time.Minute)
	if !ok {
		return fmt.Errorf("first turn did not complete")
	}

	step("shell")
	if _, err := call("thread/shellCommand", map[string]any{"threadId": id, "command": "echo shell-ok"}); err == nil {
		waitTurn(30 * time.Second)
	}

	step("compact")
	if _, err := call(codexappserver.MethodThreadCompact, codexappserver.ThreadIDParams{ThreadID: id}); err == nil {
		waitTurn(3 * time.Minute)
	}

	step("review")
	if _, err := call("review/start", map[string]any{"threadId": id,
		"target": map[string]any{"type": "custom", "instructions": "Reply with the word LGTM. Do not run any tools."}}); err == nil {
		waitTurn(3 * time.Minute)
	}

	step("fork")
	_, _ = call(codexappserver.MethodThreadFork, codexappserver.ThreadForkParams{ThreadID: id})
	_, _ = call(codexappserver.MethodThreadFork, codexappserver.ThreadForkParams{ThreadID: id, LastTurnID: first.ID})

	step("skills and account")
	_, _ = call("skills/list", map[string]any{})
	_, _ = call("account/read", map[string]any{})
	_, _ = call("account/rateLimits/read", map[string]any{})

	step("stored history")
	reads := []struct {
		file, method string
		params       any
	}{
		{"thread-read.json", codexappserver.MethodThreadRead, codexappserver.ThreadReadParams{ThreadID: id}},
		{"turns.json", codexappserver.MethodThreadTurns, codexappserver.ThreadTurnsListParams{ThreadID: id, SortDirection: "asc", ItemsView: "notLoaded"}},
		{"items.json", codexappserver.MethodThreadItems, codexappserver.ThreadItemsListParams{ThreadID: id, SortDirection: "asc"}},
	}
	var path string
	for _, r := range reads {
		raw, err := call(r.method, r.params)
		if err != nil {
			return err
		}
		if r.method == codexappserver.MethodThreadRead {
			var read codexappserver.ThreadReadResponse
			_ = json.Unmarshal(raw, &read)
			path = read.Thread.Path
		}
		if err := writeJSON(filepath.Join(out, r.file), raw, rec.scrub); err != nil {
			return err
		}
	}
	// The rollout holds per-turn token usage the app-server does not serve.
	if path == "" {
		return fmt.Errorf("thread/read returned no rollout path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_ = home
	return os.WriteFile(filepath.Join(out, "rollout.jsonl"), rec.scrub(data), 0o644)
}
