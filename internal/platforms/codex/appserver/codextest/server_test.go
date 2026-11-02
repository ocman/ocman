package codextest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func TestServer(t *testing.T) {
	s, pipes := New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := codexappserver.Attach(ctx, pipes.Reader, pipes.Writer, codexappserver.Options{})
	if err != nil {
		t.Fatal(err)
	}

	s.Handle("ok", func(json.RawMessage) (any, error) { return map[string]int{"n": 1}, nil })
	s.Handle("rpc", func(json.RawMessage) (any, error) { return nil, &codexappserver.RPCError{Code: -1, Message: "rpc"} })
	s.Handle("plain", func(json.RawMessage) (any, error) { return nil, errors.New("plain") })
	s.Handle("unencodable", func(json.RawMessage) (any, error) { return make(chan int), nil })
	var out struct{ N int }
	if err := c.Call(ctx, "ok", nil, &out); err != nil || out.N != 1 {
		t.Fatalf("ok = %v %+v", err, out)
	}
	for method, code := range map[string]int{"rpc": -1, "plain": -32603, "unencodable": -32603, "missing": -32601} {
		var rpc *codexappserver.RPCError
		if err := c.Call(ctx, method, nil, nil); !errors.As(err, &rpc) || rpc.Code != code {
			t.Errorf("%s err = %v", method, err)
		}
	}
	if calls := s.Calls(); len(calls) != 6 || calls[1].Method != "ok" {
		t.Fatalf("calls = %+v", calls)
	}

	if err := s.Notify("n", map[string]int{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Notify("n", make(chan int)); err == nil {
		t.Fatal("unencodable notification sent")
	}
	if _, err := s.Request("r", make(chan int)); err == nil {
		t.Fatal("unencodable request sent")
	}
	// The client refuses every server request, whatever its ID.
	reply, err := s.Request("r", map[string]int{})
	if err != nil {
		t.Fatal(err)
	}
	if r := <-reply; r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("reply = %+v", r)
	}
	reply, err = s.RequestID(json.RawMessage(`"s1"`), "r", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := <-reply; r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("string-ID reply = %+v", r)
	}

	_ = c.Close()
	select {
	case <-s.Done():
	case <-ctx.Done():
		t.Fatal("server did not see the client close")
	}
	if notes := s.Notifications(); len(notes) != 1 || notes[0] != "initialized" {
		t.Fatalf("notifications = %q", notes)
	}
}
