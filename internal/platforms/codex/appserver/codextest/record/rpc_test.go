//go:build codexrecord

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// peer is the app-server side of an rpc under test.
type peer struct {
	t    *testing.T
	c    *rpc
	in   *bufio.Scanner // what the client sent
	out  *io.PipeWriter // what the client reads
	rec  *recorder
	done chan struct{} // closed when the client's reader returns
}

func newPeer(t *testing.T) *peer {
	t.Helper()
	sentR, sentW := io.Pipe()
	recvR, recvW := io.Pipe()
	f, err := os.Create(filepath.Join(t.TempDir(), "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	p := &peer{t: t, c: newRPC(sentW), in: bufio.NewScanner(sentR), out: recvW,
		rec: &recorder{f: f, scrub: func(b []byte) []byte { return b }}, done: make(chan struct{})}
	go func() { p.c.read(recvR, p.rec); close(p.done) }()
	t.Cleanup(func() { _ = recvW.Close(); _ = sentR.Close() })
	return p
}

// next returns the next message the client sent.
func (p *peer) next() rpcMessage {
	p.t.Helper()
	got := make(chan rpcMessage, 1)
	go func() {
		var m rpcMessage
		if p.in.Scan() {
			_ = json.Unmarshal(p.in.Bytes(), &m)
		}
		got <- m
	}()
	select {
	case m := <-got:
		return m
	case <-time.After(5 * time.Second):
		p.t.Fatal("client sent nothing")
		return rpcMessage{}
	}
}

func (p *peer) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.out, line+"\n"); err != nil {
		p.t.Fatal(err)
	}
}

// call starts c.call in the background and returns its result channel.
func (p *peer) call(ctx context.Context, method string, out any) chan error {
	done := make(chan error, 1)
	go func() { done <- p.c.call(ctx, method, map[string]int{"n": 1}, out) }()
	return done
}

func wait(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("call did not return")
		return nil
	}
}

func TestRPCResponses(t *testing.T) {
	p := newPeer(t)
	var out struct{ N int }
	done := p.call(t.Context(), "numeric", &out)
	req := p.next()
	if req.Method != "numeric" || string(req.Params) != `{"n":1}` {
		t.Fatalf("request = %+v", req)
	}
	p.send(`{"id":` + string(req.ID) + `,"result":{"N":7}}`)
	if err := wait(t, done); err != nil || out.N != 7 {
		t.Fatalf("numeric = %v %+v", err, out)
	}

	// A string-echoed ID still matches its numeric request.
	done = p.call(t.Context(), "string", nil)
	req = p.next()
	p.send(`{"id":"` + string(req.ID) + `","result":{}}`)
	if err := wait(t, done); err != nil {
		t.Fatalf("string ID = %v", err)
	}

	done = p.call(t.Context(), "failing", nil)
	req = p.next()
	p.send(`{"id":` + string(req.ID) + `,"error":{"code":-32600,"message":"nope"}}`)
	var rpcErr *codexappserver.RPCError
	if err := wait(t, done); !errors.As(err, &rpcErr) || rpcErr.Code != -32600 {
		t.Fatalf("error = %v", err)
	}

	// Every received line is recorded.
	data, err := os.ReadFile(p.rec.f.Name())
	if err != nil || strings.Count(string(data), `"dir":"recv"`) != 3 {
		t.Fatalf("transcript = %s %v", data, err)
	}
}

func TestRPCCancellationAndEOF(t *testing.T) {
	p := newPeer(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := p.call(ctx, "slow", nil)
	p.next()
	cancel()
	if err := wait(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}
	done = p.call(t.Context(), "pending", nil)
	p.next()
	_ = p.out.Close()
	if err := wait(t, done); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("pending at EOF = %v", err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not stop at EOF")
	}
}

func TestRPCTurnsAndApprovals(t *testing.T) {
	p := newPeer(t)
	p.send(`{"method":"turn/completed","params":{"threadId":"thr","turn":{"id":"t1","status":"completed","items":[]}}}`)
	select {
	case turn := <-p.c.turns:
		if turn.ID != "t1" || turn.Status != codexappserver.TurnCompleted {
			t.Fatalf("turn = %+v", turn)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn/completed was not delivered")
	}

	for _, tc := range []struct {
		request, want string
	}{
		{`{"id":1,"method":"item/commandExecution/requestApproval","params":{"command":"ls"}}`, `{"decision":"accept"}`},
		{`{"id":2,"method":"item/fileChange/requestApproval","params":{}}`, `{"decision":"accept"}`},
		{`{"id":3,"method":"item/commandExecution/requestApproval","params":{"command":"sleep 30"}}`, `{"decision":"cancel"}`},
	} {
		p.send(tc.request)
		if reply := p.next(); string(reply.Result) != tc.want {
			t.Fatalf("%s: reply = %+v", tc.request, reply)
		}
	}
	p.send(`{"id":"q","method":"item/tool/requestUserInput","params":{}}`)
	if reply := p.next(); reply.Error == nil || reply.Error.Code != -32601 || string(reply.ID) != `"q"` {
		t.Fatalf("unsupported request reply = %+v", reply)
	}
}

func TestRPCInitializeAndStartTurn(t *testing.T) {
	p := newPeer(t)
	done := make(chan error, 1)
	go func() { done <- p.c.initialize(t.Context()) }()
	req := p.next()
	var params codexappserver.InitializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil || req.Method != codexappserver.MethodInitialize ||
		params.ClientInfo.Version != "record" || !params.Capabilities.ExperimentalAPI {
		t.Fatalf("initialize = %+v %v", req, err)
	}
	p.send(`{"id":` + string(req.ID) + `,"result":{}}`)
	if note := p.next(); note.Method != codexappserver.MethodInitialized || note.ID != nil {
		t.Fatalf("initialized = %+v", note)
	}
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}

	go func() { done <- p.c.startTurn(t.Context(), "thr", "hi") }()
	req = p.next()
	if req.Method != "turn/start" || string(req.Params) != `{"input":[{"text":"hi","text_elements":[],"type":"text"}],"threadId":"thr"}` {
		t.Fatalf("turn/start = %s %s", req.Method, req.Params)
	}
	p.send(`{"id":` + string(req.ID) + `,"error":{"code":-32600,"message":"busy"}}`)
	if err := wait(t, done); err == nil {
		t.Fatal("turn/start error ignored")
	}

	go func() { done <- p.c.initialize(t.Context()) }()
	req = p.next()
	p.send(`{"id":` + string(req.ID) + `,"error":{"code":-1,"message":"denied"}}`)
	if err := wait(t, done); err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("failed initialize = %v", err)
	}
}
