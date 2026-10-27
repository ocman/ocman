package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// peer is the server end of a pipe pair: it reads what the client wrote and
// writes raw lines back.
type peer struct {
	t     *testing.T
	in    chan []byte
	out   *io.PipeWriter
	conn  *Conn
	close func()
}

type recorder struct {
	mu       sync.Mutex
	notes    []string
	requests []string
	ids      []json.RawMessage
	got      chan struct{}
}

func newRecorder() *recorder { return &recorder{got: make(chan struct{}, 16)} }

func (r *recorder) Notification(method string, params json.RawMessage) {
	r.mu.Lock()
	r.notes = append(r.notes, method+" "+string(params))
	r.mu.Unlock()
	r.got <- struct{}{}
}

func (r *recorder) Request(id json.RawMessage, method string, params json.RawMessage) {
	r.mu.Lock()
	r.requests = append(r.requests, method+" "+string(params))
	r.ids = append(r.ids, id)
	r.mu.Unlock()
	r.got <- struct{}{}
}

func newPeer(t *testing.T, h Handler) *peer {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	conn := NewConn(clientR, clientW, h)
	p := &peer{t: t, in: make(chan []byte, 64), out: serverW, conn: conn}
	go func() {
		scanner := bufio.NewScanner(serverR)
		for scanner.Scan() {
			p.in <- append([]byte(nil), scanner.Bytes()...)
		}
		close(p.in)
	}()
	p.close = func() { _ = serverW.Close(); _ = serverR.Close() }
	t.Cleanup(p.close)
	return p
}

func (p *peer) read() map[string]any {
	p.t.Helper()
	var line []byte
	select {
	case l, ok := <-p.in:
		if !ok {
			p.t.Fatal("peer input closed")
		}
		line = l
	case <-time.After(5 * time.Second):
		p.t.Fatal("peer read timed out")
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		p.t.Fatalf("peer decode %q: %v", line, err)
	}
	return m
}

func (p *peer) write(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.out, line+"\n"); err != nil {
		p.t.Fatal(err)
	}
}

func wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestCallRoundTrip(t *testing.T) {
	p := newPeer(t, nil)
	type result struct {
		Name string `json:"name"`
	}
	done := make(chan error, 1)
	var got result
	go func() {
		done <- p.conn.Call(context.Background(), "thread/start", map[string]string{"cwd": "/x"}, &got)
	}()
	req := p.read()
	if req["method"] != "thread/start" || req["params"].(map[string]any)["cwd"] != "/x" {
		t.Fatalf("request = %v", req)
	}
	if _, ok := req["jsonrpc"]; ok {
		t.Fatalf("request carries jsonrpc member: %v", req)
	}
	// The response echoes the ID as a string: it still matches.
	p.write(`{"id":"` + jsonNumber(req["id"]) + `","result":{"name":"ok"}}`)
	if err := <-done; err != nil || got.Name != "ok" {
		t.Fatalf("Call = %v, %+v", err, got)
	}
}

func jsonNumber(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func TestCallErrorResponse(t *testing.T) {
	p := newPeer(t, nil)
	done := make(chan error, 1)
	go func() { done <- p.conn.Call(context.Background(), "turn/start", nil, nil) }()
	req := p.read()
	if _, ok := req["params"]; ok {
		t.Fatalf("nil params were sent: %v", req)
	}
	p.write(`{"id":` + jsonNumber(req["id"]) + `,"error":{"code":-32600,"message":"busy","data":{"reason":"x"}}}`)
	err := <-done
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32600 || rpc.Message != "busy" || string(rpc.Data) != `{"reason":"x"}` {
		t.Fatalf("err = %#v", err)
	}
	if !strings.Contains(err.Error(), "busy") {
		t.Fatalf("message = %q", err)
	}
}

func TestCallBadResult(t *testing.T) {
	p := newPeer(t, nil)
	done := make(chan error, 1)
	var out struct{ N int }
	go func() { done <- p.conn.Call(context.Background(), "m", struct{}{}, &out) }()
	req := p.read()
	p.write(`{"id":` + jsonNumber(req["id"]) + `,"result":{"N":"nope"}}`)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "decoding m result") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallContextCancel(t *testing.T) {
	p := newPeer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.conn.Call(ctx, "m", nil, nil) }()
	p.read()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || errors.Is(err, ErrNotSent) {
		t.Fatalf("err = %v", err)
	}
	// A late response for the abandoned call is dropped harmlessly.
	p.write(`{"id":1,"result":{}}`)
}

// A peer that never reads must not hold a call past its context, whether the
// call's frame is stuck on the wire or queued behind another stuck frame.
func TestCallUnreadPeerHonoursContext(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	conn := NewConn(clientR, clientW, nil)
	t.Cleanup(func() { _ = serverW.Close(); _ = serverR.Close() })
	call := func(ctx context.Context) error {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- conn.Call(ctx, "m", nil, nil) }()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("Call ignored its context")
			return nil
		}
	}
	short := func() context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		t.Cleanup(cancel)
		return ctx
	}
	if err := call(short()); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrNotSent) {
		t.Fatalf("stuck call err = %v", err)
	}
	if err := call(short()); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrNotSent) {
		t.Fatalf("queued call err = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := call(cancelled); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrNotSent) {
		t.Fatalf("cancelled call err = %v", err)
	}
	// Once the peer reads again, it sees whole frames only.
	scanner := bufio.NewScanner(serverR)
	if !scanner.Scan() {
		t.Fatal("no frame")
	}
	var m map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &m); err != nil || m["method"] != "m" {
		t.Fatalf("frame %q: %v", scanner.Bytes(), err)
	}
}

// A failed write ends the connection, releasing senders queued behind it.
func TestWriteFailureReleasesQueuedSenders(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	conn := NewConn(clientR, clientW, nil)
	t.Cleanup(func() { _ = serverW.Close() })
	stuck := make(chan error, 1)
	go func() { stuck <- conn.Notify("first", nil) }()
	queued := make(chan error, 1)
	go func() { queued <- conn.Notify("second", nil) }()
	time.Sleep(20 * time.Millisecond) // let one frame reach the wire and the other queue
	_ = serverR.Close()
	for _, ch := range []chan error{stuck, queued} {
		select {
		case err := <-ch:
			if !errors.Is(err, ErrClosed) {
				t.Fatalf("err = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("sender was not released")
		}
	}
	wait(t, conn.Done())
}

// The peer hangs up while a frame is stuck on the wire: its sender is released.
func TestPeerHangUpReleasesStuckSender(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	conn := NewConn(clientR, clientW, nil)
	t.Cleanup(func() { _ = serverR.Close() })
	stuck := make(chan error, 1)
	go func() { stuck <- conn.Notify("m", nil) }()
	time.Sleep(20 * time.Millisecond) // let the frame reach the wire
	_ = serverW.Close()
	select {
	case err := <-stuck:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sender was not released")
	}
}

func TestCallUnencodableParams(t *testing.T) {
	p := newPeer(t, nil)
	if err := p.conn.Call(context.Background(), "m", make(chan int), nil); err == nil {
		t.Fatal("expected an encoding error")
	}
	if err := p.conn.Notify("m", make(chan int)); err == nil {
		t.Fatal("expected an encoding error")
	}
	if err := p.conn.Reply(json.RawMessage("1"), make(chan int)); err == nil {
		t.Fatal("expected an encoding error")
	}
}

func TestNotificationsAndRequestsInOrder(t *testing.T) {
	rec := newRecorder()
	p := newPeer(t, rec)
	p.write(`not json`)
	p.write(``)
	p.write(`{"method":"turn/started","params":{"n":1}}`)
	p.write(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"n":2}}`)
	p.write(`{"method":"item/completed","params":{"n":3},"id":null}`)
	for range 3 {
		wait(t, rec.got)
	}
	rec.mu.Lock()
	notes, requests, ids := rec.notes, rec.requests, rec.ids
	rec.mu.Unlock()
	if len(notes) != 2 || notes[0] != `turn/started {"n":1}` || notes[1] != `item/completed {"n":3}` {
		t.Fatalf("notes = %q", notes)
	}
	if len(requests) != 1 || requests[0] != `item/commandExecution/requestApproval {"n":2}` || string(ids[0]) != "7" {
		t.Fatalf("requests = %q ids = %q", requests, ids)
	}
	if err := p.conn.Reply(ids[0], map[string]string{"decision": "accept"}); err != nil {
		t.Fatal(err)
	}
	reply := p.read()
	if reply["id"] != float64(7) || reply["result"].(map[string]any)["decision"] != "accept" {
		t.Fatalf("reply = %v", reply)
	}
	if err := p.conn.Reply(json.RawMessage("8"), nil); err != nil {
		t.Fatal(err)
	}
	if reply := p.read(); reply["id"] != float64(8) || len(reply["result"].(map[string]any)) != 0 {
		t.Fatalf("empty reply = %v", reply)
	}
	if err := p.conn.ReplyError(json.RawMessage(`"s"`), -32000, "denied"); err != nil {
		t.Fatal(err)
	}
	if reply := p.read(); reply["id"] != "s" || reply["error"].(map[string]any)["message"] != "denied" {
		t.Fatalf("error reply = %v", reply)
	}
	if err := p.conn.Notify("initialized", nil); err != nil {
		t.Fatal(err)
	}
	if note := p.read(); note["method"] != "initialized" || note["id"] != nil {
		t.Fatalf("notification = %v", note)
	}
}

func TestRequestWithoutHandlerIsRejected(t *testing.T) {
	p := newPeer(t, nil)
	p.write(`{"id":3,"method":"item/tool/requestUserInput","params":{}}`)
	p.write(`{"method":"ignored"}`)
	reply := p.read()
	if reply["id"] != float64(3) || reply["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("reply = %v", reply)
	}
}

func TestCloseFailsPendingAndLaterCalls(t *testing.T) {
	p := newPeer(t, nil)
	done := make(chan error, 1)
	go func() { done <- p.conn.Call(context.Background(), "m", nil, nil) }()
	p.read()
	_ = p.out.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("pending err = %v", err)
	}
	wait(t, p.conn.Done())
	if !errors.Is(p.conn.Err(), ErrClosed) {
		t.Fatalf("Err = %v", p.conn.Err())
	}
	if err := p.conn.Call(context.Background(), "m", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("later call err = %v", err)
	}
	if err := p.conn.Notify("m", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("later notify err = %v", err)
	}
	p.conn.close(errors.New("second close is ignored"))
	if !errors.Is(p.conn.Err(), ErrClosed) {
		t.Fatalf("Err after second close = %v", p.conn.Err())
	}
}

func TestWriteFailure(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	conn := NewConn(clientR, clientW, nil)
	t.Cleanup(func() { _ = serverW.Close() })
	_ = serverR.Close()
	if err := conn.Notify("m", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestOversizedLineEndsConnection(t *testing.T) {
	p := newPeer(t, nil)
	go func() { _, _ = io.WriteString(p.out, strings.Repeat("x", maxLine+1)) }()
	wait(t, p.conn.Done())
	if err := p.conn.Err(); !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err = %v", err)
	}
}
