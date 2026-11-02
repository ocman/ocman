package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

func newPeer(t *testing.T) *peer {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	conn := newConn(clientR, clientW)
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
	p := newPeer(t)
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
	p := newPeer(t)
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
	p := newPeer(t)
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
	p := newPeer(t)
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
	conn := newConn(clientR, clientW)
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
	conn := newConn(clientR, clientW)
	t.Cleanup(func() { _ = serverW.Close() })
	stuck := make(chan error, 1)
	go func() { stuck <- conn.notify("first", nil) }()
	queued := make(chan error, 1)
	go func() { queued <- conn.notify("second", nil) }()
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
	conn := newConn(clientR, clientW)
	t.Cleanup(func() { _ = serverR.Close() })
	stuck := make(chan error, 1)
	go func() { stuck <- conn.notify("m", nil) }()
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
	p := newPeer(t)
	if err := p.conn.Call(context.Background(), "m", make(chan int), nil); err == nil {
		t.Fatal("expected an encoding error")
	}
	if err := p.conn.notify("m", make(chan int)); err == nil {
		t.Fatal("expected an encoding error")
	}
}

// Server requests are refused whatever their ID; notifications and stray
// output are ignored.
func TestServerTrafficIsIgnoredOrRefused(t *testing.T) {
	p := newPeer(t)
	p.write(`not json`)
	p.write(``)
	p.write(`{"method":"turn/started","params":{"n":1}}`)
	p.write(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"n":2}}`)
	p.write(`{"method":"item/completed","params":{"n":3},"id":null}`)
	p.write(`{"id":"s","method":"item/tool/requestUserInput","params":{}}`)
	got := map[any]any{}
	for range 2 {
		reply := p.read()
		got[reply["id"]] = reply["error"].(map[string]any)["code"]
	}
	if len(got) != 2 || got[float64(7)] != float64(-32601) || got["s"] != float64(-32601) {
		t.Fatalf("refusals = %v", got)
	}
	if err := p.conn.notify("initialized", nil); err != nil {
		t.Fatal(err)
	}
	if note := p.read(); note["method"] != "initialized" || note["id"] != nil {
		t.Fatalf("notification = %v", note)
	}
}

func TestCloseFailsPendingAndLaterCalls(t *testing.T) {
	p := newPeer(t)
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
	if err := p.conn.notify("m", nil); !errors.Is(err, ErrClosed) {
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
	conn := newConn(clientR, clientW)
	t.Cleanup(func() { _ = serverW.Close() })
	_ = serverR.Close()
	if err := conn.notify("m", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestOversizedLineEndsConnection(t *testing.T) {
	p := newPeer(t)
	go func() { _, _ = io.WriteString(p.out, strings.Repeat("x", maxLine+1)) }()
	wait(t, p.conn.Done())
	if err := p.conn.Err(); !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err = %v", err)
	}
}

// A duplicate response for a call that has not consumed its first one must
// never block the single reader goroutine.
func TestDuplicateResponseDoesNotStallReader(t *testing.T) {
	p := newPeer(t)
	p.conn.mu.Lock()
	p.conn.pending["7"] = make(chan response, 1)
	p.conn.mu.Unlock()
	go func() {
		_, _ = io.WriteString(p.out, "{\"id\":7,\"result\":{}}\n{\"id\":7,\"result\":{}}\n{\"id\":8,\"method\":\"after\"}\n")
	}()
	if reply := p.read(); reply["id"] != float64(8) {
		t.Fatalf("reader stalled before %v", reply)
	}
}

// A response the caller never consumed must not block close, or Done never
// closes and every later caller hangs.
func TestUnconsumedResponseDoesNotBlockClose(t *testing.T) {
	p := newPeer(t)
	ch := make(chan response, 1)
	ch <- response{result: json.RawMessage(`{}`)}
	p.conn.mu.Lock()
	p.conn.pending["7"] = ch
	p.conn.mu.Unlock()
	_ = p.out.Close()
	wait(t, p.conn.Done())
}

// A peer that stops draining stdin must not wedge a refusal forever.
func TestReplyToStalledPeerIsBounded(t *testing.T) {
	restore := replyTimeout
	replyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { replyTimeout = restore })
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	conn := newConn(clientR, clientW)
	t.Cleanup(func() { _ = serverW.Close(); _ = serverR.Close() })
	done := make(chan error, 1)
	go func() { done <- conn.replyError(json.RawMessage("2"), -32601, "no") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refusal to a stalled peer never returned")
	}
}
