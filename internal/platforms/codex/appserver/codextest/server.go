// Package codextest provides an in-process fake `codex app-server` for tests.
// Tests never run the real Codex CLI.
package codextest

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// Method answers one client request. Returning a *codexappserver.RPCError
// sends it as the error response.
type Method func(params json.RawMessage) (any, error)

// Call is one request the client made.
type Call struct {
	Method string
	Params json.RawMessage
}

// Reply is the client's answer to a server-initiated request.
type Reply struct {
	Result json.RawMessage
	Error  *codexappserver.RPCError
}

// Pipes is the client side of a fake server.
type Pipes struct {
	Reader io.Reader
	Writer io.WriteCloser
}

// Info is the default initialize response.
var Info = codexappserver.InitializeResponse{UserAgent: "codex_cli_rs/0.0.0-test", CodexHome: "/fake/.codex", PlatformFamily: "unix", PlatformOS: "macos"}

// Server is the server end of a pipe pair. Unregistered methods are rejected
// with -32601.
type Server struct {
	w       io.Writer
	writeMu sync.Mutex
	done    chan struct{}

	mu      sync.Mutex
	methods map[string]Method
	calls   []Call
	notes   []string
	replies map[string]chan Reply
	nextID  int
}

type message struct {
	ID     json.RawMessage          `json:"id,omitempty"`
	Method string                   `json:"method,omitempty"`
	Params json.RawMessage          `json:"params,omitempty"`
	Result json.RawMessage          `json:"result,omitempty"`
	Error  *codexappserver.RPCError `json:"error,omitempty"`
}

// New starts a fake server and returns the client's pipe ends. initialize is
// answered with Info until a test replaces it.
func New(t testing.TB) (*Server, Pipes) {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	s := Serve(serverR, serverW)
	t.Cleanup(func() { _ = serverW.Close(); _ = serverR.Close() })
	return s, Pipes{Reader: clientR, Writer: clientW}
}

// Serve runs a fake server on an existing stream, such as a helper process's
// stdio. Setup callbacks register handlers before any request can be read.
func Serve(r io.Reader, w io.Writer, setup ...func(*Server)) *Server {
	s := &Server{w: w, done: make(chan struct{}), methods: map[string]Method{}, replies: map[string]chan Reply{}}
	s.Handle(codexappserver.MethodInitialize, func(json.RawMessage) (any, error) { return Info, nil })
	for _, configure := range setup {
		configure(s)
	}
	go s.read(r)
	return s
}

// Handle registers or replaces a method.
func (s *Server) Handle(method string, fn Method) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[method] = fn
}

// Calls returns the requests received so far, in order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Notifications returns the notification methods received so far.
func (s *Server) Notifications() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.notes...)
}

// Done is closed when the client closes its end.
func (s *Server) Done() <-chan struct{} { return s.done }

// Notify sends a notification to the client.
func (s *Server) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return s.write(message{Method: method, Params: raw})
}

// Request sends a server-initiated request and returns a channel that
// receives the client's reply.
func (s *Server) Request(method string, params any) (<-chan Reply, error) {
	s.mu.Lock()
	s.nextID++
	id := strconv.Itoa(1000 + s.nextID)
	s.mu.Unlock()
	return s.RequestID(json.RawMessage(id), method, params)
}

// RequestID is Request with an explicit ID, such as a string.
func (s *Server) RequestID(id json.RawMessage, method string, params any) (<-chan Reply, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan Reply, 1)
	s.mu.Lock()
	s.replies[string(id)] = ch
	s.mu.Unlock()
	return ch, s.write(message{ID: id, Method: method, Params: raw})
}

func (s *Server) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.w.Write(append(data, '\n'))
	return err
}

// read serves until the client closes its end. Like the real process, the
// server then exits, closing its output.
func (s *Server) read(r io.Reader) {
	defer close(s.done)
	defer func() {
		if c, ok := s.w.(io.Closer); ok {
			s.writeMu.Lock()
			_ = c.Close()
			s.writeMu.Unlock()
		}
	}()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 64<<20)
	for scanner.Scan() {
		var m message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && m.ID != nil:
			s.serve(m)
		case m.Method != "":
			s.mu.Lock()
			s.notes = append(s.notes, m.Method)
			s.mu.Unlock()
		default:
			s.mu.Lock()
			ch := s.replies[string(m.ID)]
			delete(s.replies, string(m.ID))
			s.mu.Unlock()
			if ch != nil {
				ch <- Reply{Result: m.Result, Error: m.Error}
			}
		}
	}
}

func (s *Server) serve(m message) {
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: m.Method, Params: m.Params})
	fn := s.methods[m.Method]
	s.mu.Unlock()
	go func() {
		if fn == nil {
			_ = s.write(message{ID: m.ID, Error: &codexappserver.RPCError{Code: -32601, Message: "method not found: " + m.Method}})
			return
		}
		result, err := fn(m.Params)
		var rpc *codexappserver.RPCError
		switch {
		case err == nil:
			raw, merr := json.Marshal(result)
			if merr != nil {
				rpc = &codexappserver.RPCError{Code: -32603, Message: merr.Error()}
				_ = s.write(message{ID: m.ID, Error: rpc})
				return
			}
			_ = s.write(message{ID: m.ID, Result: raw})
		case errors.As(err, &rpc):
			_ = s.write(message{ID: m.ID, Error: rpc})
		default:
			_ = s.write(message{ID: m.ID, Error: &codexappserver.RPCError{Code: -32603, Message: err.Error()}})
		}
	}()
}
