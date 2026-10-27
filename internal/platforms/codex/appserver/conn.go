// Package codexappserver speaks the JSON-RPC protocol of `codex app-server`
// over its stdio pipes. Messages are newline-delimited JSON objects shaped like
// JSON-RPC 2.0 without the "jsonrpc" member.
package codexappserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// maxLine bounds one inbound message. Item payloads such as command output and
// file diffs can be large, so this is generous.
const maxLine = 64 << 20

// ErrClosed reports a call on a connection whose peer has gone away.
var ErrClosed = errors.New("codex app-server connection closed")

// ErrNotSent marks failures before a request reaches the writer. Once the
// writer takes a frame, cancellation cannot prove that the peer rejected it.
// The original cause remains available through errors.Is and errors.As.
var ErrNotSent = errors.New("codex app-server request was not sent")

// RPCError is a JSON-RPC error response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

// Handler receives server-initiated traffic. Both methods are called from the
// connection's single reader goroutine in arrival order, so they must not
// block or make calls on the same connection. A request is answered later
// with Conn.Reply or Conn.ReplyError, using the ID it was delivered with.
type Handler interface {
	Notification(method string, params json.RawMessage)
	Request(id json.RawMessage, method string, params json.RawMessage)
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    error
}

// frame is one encoded message handed to the writer goroutine.
type frame struct {
	data   []byte
	result chan error
}

// Conn is one client connection. Calls are safe for concurrent use.
type Conn struct {
	w       io.Writer
	handler Handler

	// writes feeds the single writer goroutine. Only it touches w, so a frame
	// is always written whole and a stuck write never holds a lock that a
	// cancellable sender waits on.
	writes chan frame

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan response
	err     error
	done    chan struct{}
}

// NewConn starts reading r and writes requests to w. The connection ends when
// r reaches EOF or fails, or a write to w fails; Done is closed and pending
// calls fail with ErrClosed.
func NewConn(r io.Reader, w io.Writer, handler Handler) *Conn {
	c := &Conn{w: w, handler: handler, writes: make(chan frame), pending: map[string]chan response{}, done: make(chan struct{})}
	go c.read(r)
	go c.write()
	return c
}

// Done is closed once the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is the reason the connection ended, or nil while it is open.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Call sends a request and decodes its result into out, which may be nil.
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	raw, err := encode(params)
	if err != nil {
		return errors.Join(ErrNotSent, err)
	}
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return errors.Join(ErrNotSent, c.err)
	}
	c.nextID++
	id := strconv.FormatInt(c.nextID, 10)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(ctx, message{ID: json.RawMessage(id), Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-ch:
		if r.err != nil || out == nil || len(r.result) == 0 {
			return r.err
		}
		if err := json.Unmarshal(r.result, out); err != nil {
			return fmt.Errorf("decoding %s result: %w", method, err)
		}
		return nil
	}
}

// Notify sends a notification, which has no response.
func (c *Conn) Notify(method string, params any) error {
	raw, err := encode(params)
	if err != nil {
		return err
	}
	return c.send(context.Background(), message{Method: method, Params: raw})
}

// Reply answers a server-initiated request.
func (c *Conn) Reply(id json.RawMessage, result any) error {
	raw, err := encode(result)
	if err != nil {
		return err
	}
	if raw == nil {
		raw = json.RawMessage("{}")
	}
	return c.send(context.Background(), message{ID: id, Result: raw})
}

// ReplyError rejects a server-initiated request.
func (c *Conn) ReplyError(id json.RawMessage, code int, msg string) error {
	return c.send(context.Background(), message{ID: id, Error: &RPCError{Code: code, Message: msg}})
}

// send hands m to the writer goroutine and waits for it to be written. ctx
// bounds both the wait for the writer and the write itself; a frame the
// writer has already taken is still written whole after send returns.
func (c *Conn) send(ctx context.Context, m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return errors.Join(ErrNotSent, err)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrNotSent, err)
	}
	if err := c.Err(); err != nil {
		return errors.Join(ErrNotSent, err)
	}
	f := frame{data: append(data, '\n'), result: make(chan error, 1)}
	select {
	case c.writes <- f:
	case <-ctx.Done():
		return errors.Join(ErrNotSent, ctx.Err())
	case <-c.done:
		return errors.Join(ErrNotSent, c.Err())
	}
	select {
	case err := <-f.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
}

// write is the only writer of c.w. A failed write may have sent part of a
// frame, so it ends the connection rather than letting another frame follow.
func (c *Conn) write() {
	for {
		select {
		case f := <-c.writes:
			if _, err := c.w.Write(f.data); err != nil {
				err = fmt.Errorf("%w: %w", ErrClosed, err)
				f.result <- err
				c.close(err)
				return
			}
			f.result <- nil
		case <-c.done:
			return
		}
	}
}

func (c *Conn) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var m message
		if json.Unmarshal(line, &m) != nil {
			continue // stray output must not end the connection
		}
		c.dispatch(m)
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.close(fmt.Errorf("%w: %w", ErrClosed, err))
}

func (c *Conn) dispatch(m message) {
	switch {
	case m.Method != "" && hasID(m.ID):
		if c.handler == nil {
			_ = c.ReplyError(m.ID, -32601, "method not found")
			return
		}
		c.handler.Request(m.ID, m.Method, m.Params)
	case m.Method != "":
		if c.handler != nil {
			c.handler.Notification(m.Method, m.Params)
		}
	case hasID(m.ID):
		c.mu.Lock()
		ch := c.pending[idKey(m.ID)]
		c.mu.Unlock()
		if ch == nil {
			return
		}
		if m.Error != nil {
			ch <- response{err: m.Error}
			return
		}
		ch <- response{result: m.Result}
	}
}

func (c *Conn) close(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	pending := c.pending
	c.pending = map[string]chan response{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- response{err: err}
	}
	close(c.done)
}

func hasID(id json.RawMessage) bool {
	id = bytes.TrimSpace(id)
	return len(id) > 0 && !bytes.Equal(id, []byte("null"))
}

// idKey normalizes an ID so a numeric response matches its request whether
// the peer echoes it as a number or a string.
func idKey(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	return string(bytes.TrimSpace(id))
}

func encode(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(v)
}
