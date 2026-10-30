package acptest

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

func connect(t *testing.T, config Config, accept bool) *acp.Connection {
	t.Helper()
	left, right := net.Pipe()
	client := acp.NewConnection(func(_ context.Context, method string, _ json.RawMessage) (any, *acp.RequestError) {
		switch method {
		case "session/update":
			return nil, nil
		case "session/request_permission":
			id := "reject"
			if accept {
				id = "allow"
			}
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(id))}, nil
		case "elicitation/create":
			if accept {
				return map[string]any{"action": "accept", "content": map[string]any{"choice": "a"}}, nil
			}
			return map[string]any{"action": "cancel"}, nil
		}
		return nil, acp.NewMethodNotFound(method)
	}, left, left)
	done := make(chan struct{})
	go func() { Serve(right, right, config); close(done) }()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close(); <-done })
	return client
}

func TestAgent(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "accept"}[accept], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state")
			t.Setenv("FAKE_CODEX_LAUNCHES", filepath.Join(t.TempDir(), "launches"))
			client := connect(t, Config{State: path, Permission: true, Question: true}, accept)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			for _, method := range []string{"initialize", "session/new", "session/resume", "session/load", "session/fork", "session/set_config_option", "authenticate", "_session/steering"} {
				if _, err := acp.SendRequest[json.RawMessage](client, ctx, method, map[string]any{}); err != nil {
					t.Fatal(method, err)
				}
			}
			out, err := acp.SendRequest[acp.PromptResponse](client, ctx, "session/prompt", acp.PromptRequest{SessionId: "thr", Prompt: []acp.ContentBlock{acp.TextBlock("hello")}})
			if err != nil {
				t.Fatal(err)
			}
			if (out.StopReason == acp.StopReasonEndTurn) != accept {
				t.Fatal(out)
			}
			state := ReadState(path)
			if len(state.Turns) != 1 || len(state.Items) != 3 {
				t.Fatalf("%+v", state)
			}
			if _, err := acp.SendRequest[json.RawMessage](client, ctx, "unknown", nil); err == nil {
				t.Fatal("unknown method accepted")
			}
			if _, err := acp.SendRequest[json.RawMessage](client, ctx, "session/prompt", "bad"); err == nil {
				t.Fatal("invalid prompt accepted")
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	client := connect(t, Config{State: path, Hang: true}, false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := acp.SendRequest[acp.PromptResponse](client, ctx, "session/prompt", acp.PromptRequest{SessionId: "thr", Prompt: []acp.ContentBlock{acp.TextBlock("wait")}})
		result <- err
	}()
	// A notification handler barrier proves the prompt emitted its first update
	// before cancellation; use the SDK's ordered notification/response barrier.
	for len(ReadState(path).Turns) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("prompt did not start")
		case <-time.After(time.Millisecond):
		}
	}
	if err := client.SendNotification(ctx, "session/cancel", acp.CancelNotification{SessionId: "thr"}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestNativeFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if state := ReadState(path); len(state.Turns) != 0 {
		t.Fatal(state)
	}
	s, pipes := codextest.New(t)
	ConfigureNative(s, path, true)
	c, err := codexappserver.Attach(t.Context(), pipes.Reader, pipes.Writer, codexappserver.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := t.Context()
	threads, err := c.ListThreads(ctx, codexappserver.ThreadListParams{})
	if err != nil || len(threads) != 1 {
		t.Fatalf("%+v %v", threads, err)
	}
	archived := true
	threads, err = c.ListThreads(ctx, codexappserver.ThreadListParams{Archived: &archived})
	if err != nil || len(threads) != 0 {
		t.Fatalf("%+v %v", threads, err)
	}
	for _, id := range []string{codextest.StoredThread, "thr"} {
		if _, err := c.ReadThread(ctx, id, false); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListTurns(ctx, id, "notLoaded"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListItems(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.ListModels(ctx, false); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(State{Directory: "/new", CreatedAt: 100, Revision: 2})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	threads, err = c.ListThreads(ctx, codexappserver.ThreadListParams{})
	if err != nil || len(threads) != 2 || threads[1].Cwd != "/new" || threads[1].UpdatedAt != 102 {
		t.Fatalf("%+v %v", threads, err)
	}
}

func TestOwnershipRejection(t *testing.T) {
	t.Setenv("FAKE_CODEX_OWNERSHIP_CONFLICT", "1")
	client := connect(t, Config{State: filepath.Join(t.TempDir(), "state")}, false)
	if _, err := acp.SendRequest[json.RawMessage](client, t.Context(), "session/resume", map[string]any{}); err == nil {
		t.Fatal("ownership conflict ignored")
	}
}
