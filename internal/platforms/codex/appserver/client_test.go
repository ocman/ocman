package codexappserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

// TestMain doubles as a fake `codex` executable: Start runs this test binary
// with FAKE_CODEX set, which serves the fake over stdio.
func TestMain(m *testing.M) {
	switch os.Getenv("FAKE_CODEX") {
	case "":
		os.Exit(m.Run())
	case "serve":
		if len(os.Args) < 2 || os.Args[1] != "app-server" {
			os.Exit(2)
		}
		s := codextest.Serve(os.Stdin, os.Stdout)
		s.Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) {
			return map[string]any{"data": []map[string]any{{"id": "m", "model": "gpt-test", "isDefault": true}}}, nil
		})
		<-s.Done()
		os.Exit(0)
	case "fail":
		os.Exit(3)
	case "hang":
		// Read and ignore everything, then outlive stdin EOF without answering.
		_, _ = io.Copy(io.Discard, os.Stdin)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func attach(t *testing.T, opts codexappserver.Options) (*codexappserver.Client, *codextest.Server) {
	t.Helper()
	s, pipes := codextest.New(t)
	c, err := codexappserver.Attach(ctx(t), pipes.Reader, pipes.Writer, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, s
}

func params[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHandshake(t *testing.T) {
	c, s := attach(t, codexappserver.Options{ExperimentalAPI: true, OptOut: []string{"item/plan/delta"},
		Client: codexappserver.ClientInfo{Name: "ocman", Version: "1.2.3"}})
	if c.Info != codextest.Info {
		t.Fatalf("Info = %+v", c.Info)
	}
	calls := s.Calls()
	init := params[codexappserver.InitializeParams](t, calls[0].Params)
	if calls[0].Method != "initialize" || init.ClientInfo.Version != "1.2.3" || !init.Capabilities.ExperimentalAPI ||
		!slices.Equal(init.Capabilities.OptOutNotificationMethods, []string{"item/plan/delta"}) {
		t.Fatalf("initialize = %s", calls[0].Params)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(s.Notifications(), "initialized") {
		if time.Now().After(deadline) {
			t.Fatal("initialized was not sent")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHandshakeDefaultsClientInfo(t *testing.T) {
	_, s := attach(t, codexappserver.Options{})
	init := params[codexappserver.InitializeParams](t, s.Calls()[0].Params)
	if init.ClientInfo.Name != "ocman" || init.Capabilities.ExperimentalAPI {
		t.Fatalf("initialize = %s", s.Calls()[0].Params)
	}
}

func TestHandshakeFailure(t *testing.T) {
	s, pipes := codextest.New(t)
	s.Handle("initialize", func(json.RawMessage) (any, error) {
		return nil, &codexappserver.RPCError{Code: -32600, Message: "nope"}
	})
	_, err := codexappserver.Attach(ctx(t), pipes.Reader, pipes.Writer, codexappserver.Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("err = %v", err)
	}
}

func TestThreadMetadataMethods(t *testing.T) {
	c, s := attach(t, codexappserver.Options{})
	thread := map[string]any{"id": "thr_1", "cwd": "/repo", "source": "appServer", "status": map[string]any{"type": "idle"}, "turns": []any{}}
	s.Handle("thread/read", func(json.RawMessage) (any, error) { return map[string]any{"thread": thread}, nil })
	empty := func(json.RawMessage) (any, error) { return map[string]any{}, nil }
	for _, m := range []string{"thread/archive", "thread/name/set"} {
		s.Handle(m, empty)
	}
	if th, err := c.ReadThread(ctx(t), "thr_1", true); err != nil || th.Cwd != "/repo" || th.Status.Type != "idle" {
		t.Fatalf("ReadThread = %+v, %v", th, err)
	}
	if err := c.ArchiveThread(ctx(t), "thr_1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetThreadName(ctx(t), "thr_1", "title"); err != nil {
		t.Fatal(err)
	}

	calls := s.Calls()
	var methods []string
	for _, call := range calls {
		methods = append(methods, call.Method)
	}
	want := []string{"initialize", "thread/read", "thread/archive", "thread/name/set"}
	if !slices.Equal(methods, want) {
		t.Fatalf("methods = %q", methods)
	}
	if got := string(calls[1].Params); got != `{"threadId":"thr_1","includeTurns":true}` {
		t.Fatalf("thread/read params = %s", got)
	}
}

func TestReadThreadError(t *testing.T) {
	c, _ := attach(t, codexappserver.Options{})
	var rpc *codexappserver.RPCError
	if _, err := c.ReadThread(ctx(t), "x", false); !errors.As(err, &rpc) || rpc.Code != -32601 {
		t.Fatalf("err = %v", err)
	}
}

func TestListThreadsPages(t *testing.T) {
	c, s := attach(t, codexappserver.Options{})
	s.Handle("thread/list", func(raw json.RawMessage) (any, error) {
		p := params[codexappserver.ThreadListParams](t, raw)
		switch p.Cursor {
		case "":
			return map[string]any{"data": []any{map[string]any{"id": "a", "source": map[string]any{"subAgent": map[string]any{}}}}, "nextCursor": "c1"}, nil
		case "c1":
			return map[string]any{"data": []any{map[string]any{"id": "b", "source": "cli"}}, "nextCursor": nil}, nil
		}
		return nil, errors.New("bad cursor")
	})
	archived := true
	threads, err := c.ListThreads(ctx(t), codexappserver.ThreadListParams{Cursor: "ignored", Archived: &archived, SourceKinds: codexappserver.AllSourceKinds})
	if err != nil || len(threads) != 2 || threads[0].Source.Kind != "subAgent" || threads[1].ID != "b" {
		t.Fatalf("ListThreads = %+v, %v", threads, err)
	}
	if got := string(s.Calls()[1].Params); !strings.Contains(got, `"archived":true`) || !strings.Contains(got, `"subAgentThreadSpawn"`) {
		t.Fatalf("first page params = %s", got)
	}
}

func TestListThreadsRepeatedCursorAndError(t *testing.T) {
	c, s := attach(t, codexappserver.Options{})
	s.Handle("thread/list", func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{}, "nextCursor": "same"}, nil
	})
	if _, err := c.ListThreads(ctx(t), codexappserver.ThreadListParams{}); err == nil || !strings.Contains(err.Error(), "repeated cursor") {
		t.Fatalf("err = %v", err)
	}
	s.Handle("thread/list", func(json.RawMessage) (any, error) { return nil, errors.New("db") })
	if _, err := c.ListThreads(ctx(t), codexappserver.ThreadListParams{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestListTurnsAndItems(t *testing.T) {
	c, s := attach(t, codexappserver.Options{})
	s.Handle("thread/turns/list", func(raw json.RawMessage) (any, error) {
		if params[codexappserver.ThreadTurnsListParams](t, raw).Cursor == "" {
			return map[string]any{"data": []any{map[string]any{"id": "t1", "status": "completed", "items": []any{}}}, "nextCursor": "2"}, nil
		}
		return map[string]any{"data": []any{map[string]any{"id": "t2", "status": "inProgress", "items": []any{}}}, "nextCursor": nil}, nil
	})
	s.Handle("thread/items/list", func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"turnId": "t1", "startedAtMs": 5, "completedAtMs": 6,
			"item": map[string]any{"type": "agentMessage", "id": "a", "text": "hi"}}}, "nextCursor": nil}, nil
	})
	turns, err := c.ListTurns(ctx(t), "thr", "notLoaded")
	if err != nil || len(turns) != 2 || turns[1].ID != "t2" {
		t.Fatalf("ListTurns = %+v, %v", turns, err)
	}
	items, err := c.ListItems(ctx(t), "thr")
	if err != nil || len(items) != 1 || items[0].Item.Text != "hi" || items[0].StartedAtMs != 5 || items[0].TurnID != "t1" {
		t.Fatalf("ListItems = %+v, %v", items, err)
	}
	calls := s.Calls()
	if got := string(calls[1].Params); got != `{"threadId":"thr","sortDirection":"asc","itemsView":"notLoaded"}` {
		t.Errorf("turns params = %s", got)
	}
	if got := string(calls[3].Params); got != `{"threadId":"thr","sortDirection":"asc"}` {
		t.Errorf("items params = %s", got)
	}
}

func TestListModels(t *testing.T) {
	c, s := attach(t, codexappserver.Options{})
	s.Handle("model/list", func(raw json.RawMessage) (any, error) {
		if params[codexappserver.ModelListParams](t, raw).Cursor == "" {
			return map[string]any{"data": []any{map[string]any{"id": "a", "model": "gpt-a", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low"}}}}, "nextCursor": "n"}, nil
		}
		return map[string]any{"data": []any{map[string]any{"id": "b", "model": "gpt-b", "isDefault": true}}}, nil
	})
	models, err := c.ListModels(ctx(t), true)
	if err != nil || len(models) != 2 || models[0].SupportedReasoningEfforts[0].ReasoningEffort != "low" || !models[1].IsDefault {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}
	if !strings.Contains(string(s.Calls()[1].Params), `"includeHidden":true`) {
		t.Fatalf("params = %s", s.Calls()[1].Params)
	}
	s.Handle("model/list", func(json.RawMessage) (any, error) { return map[string]any{"data": []any{}, "nextCursor": "x"}, nil })
	if _, err := c.ListModels(ctx(t), false); err == nil {
		t.Fatal("expected repeated-cursor error")
	}
	s.Handle("model/list", func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	if _, err := c.ListModels(ctx(t), false); err == nil {
		t.Fatal("expected error")
	}
}

func TestStartProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	opts := codexappserver.Options{CLI: exe, Env: append(os.Environ(), "FAKE_CODEX=serve")}
	c, err := codexappserver.Start(ctx(t), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	models, err := c.ListModels(ctx(t), false)
	if err != nil || len(models) != 1 || models[0].Model != "gpt-test" {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

func TestStartProcessFailures(t *testing.T) {
	if _, err := codexappserver.Start(ctx(t), codexappserver.Options{CLI: "/nonexistent/codex"}, nil); err == nil || !strings.Contains(err.Error(), "starting codex app-server") {
		t.Fatalf("missing binary err = %v", err)
	}
	exe, _ := os.Executable()
	// The child exits before answering initialize.
	_, err := codexappserver.Start(ctx(t), codexappserver.Options{CLI: exe, Env: append(os.Environ(), "FAKE_CODEX=fail")}, nil)
	if err == nil || !errors.Is(err, codexappserver.ErrClosed) {
		t.Fatalf("early exit err = %v", err)
	}
}

// A child that never answers initialize and ignores stdin EOF is killed and
// reaped once the handshake deadline passes.
func TestStartUnresponsiveChild(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := codexappserver.Start(hctx, codexappserver.Options{CLI: exe, Env: append(os.Environ(), "FAKE_CODEX=hang")}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after its handshake deadline")
	}
}
