//go:build codexrecord

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// rpc drives the app-server being recorded. Unlike ocman's read-only client,
// it starts turns, answers approvals and waits for turn/completed.
type rpc struct {
	w       io.Writer
	writeMu sync.Mutex
	turns   chan codexappserver.Turn

	mu      sync.Mutex
	next    int
	pending map[string]chan rpcResult
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type rpcMessage struct {
	ID     json.RawMessage          `json:"id,omitempty"`
	Method string                   `json:"method,omitempty"`
	Params json.RawMessage          `json:"params,omitempty"`
	Result json.RawMessage          `json:"result,omitempty"`
	Error  *codexappserver.RPCError `json:"error,omitempty"`
}

func newRPC(w io.Writer) *rpc {
	return &rpc{w: w, turns: make(chan codexappserver.Turn, 4), pending: map[string]chan rpcResult{}}
}

func (c *rpc) write(m rpcMessage) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(append(data, '\n'))
	return err
}

func encode(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func (c *rpc) call(ctx context.Context, method string, params, out any) error {
	raw, err := encode(params)
	if err != nil {
		return err
	}
	ch := make(chan rpcResult, 1)
	c.mu.Lock()
	c.next++
	id := strconv.Itoa(c.next)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(rpcMessage{ID: json.RawMessage(id), Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-ch:
		if r.err != nil || out == nil {
			return r.err
		}
		return json.Unmarshal(r.result, out)
	}
}

func (c *rpc) initialize(ctx context.Context) error {
	params := codexappserver.InitializeParams{ClientInfo: codexappserver.ClientInfo{Name: "ocman", Version: "record"},
		Capabilities: &codexappserver.InitializeCapabilities{ExperimentalAPI: true}}
	if err := c.call(ctx, codexappserver.MethodInitialize, params, nil); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return c.write(rpcMessage{Method: codexappserver.MethodInitialized})
}

func (c *rpc) startTurn(ctx context.Context, threadID, text string) error {
	input := []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}}
	return c.call(ctx, "turn/start", map[string]any{"threadId": threadID, "input": input}, nil)
}

// read records every received line and dispatches it until stdout closes.
func (c *rpc) read(r io.Reader, rec *recorder) {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		rec.line("recv", sc.Bytes())
		var m rpcMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && m.ID != nil:
			go c.answer(m)
		case m.Method == "turn/completed":
			var n struct {
				Turn codexappserver.Turn `json:"turn"`
			}
			_ = json.Unmarshal(m.Params, &n)
			c.turns <- n.Turn
		case m.Method == "":
			id := string(m.ID)
			_ = json.Unmarshal(m.ID, &id) // a string-echoed ID matches its numeric request
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				r := rpcResult{result: m.Result}
				if m.Error != nil {
					r.err = m.Error
				}
				ch <- r
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcResult{err: io.ErrUnexpectedEOF}
		delete(c.pending, id)
	}
}

// answer accepts approvals, except the `sleep` command, which it cancels to
// interrupt that turn. Other server requests are refused.
func (c *rpc) answer(m rpcMessage) {
	switch m.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		decision := "accept"
		if strings.Contains(string(m.Params), "sleep") {
			decision = "cancel"
		}
		result, _ := json.Marshal(map[string]string{"decision": decision})
		_ = c.write(rpcMessage{ID: m.ID, Result: result})
	default:
		_ = c.write(rpcMessage{ID: m.ID, Error: &codexappserver.RPCError{Code: -32601, Message: "recorder does not answer " + m.Method}})
	}
}
