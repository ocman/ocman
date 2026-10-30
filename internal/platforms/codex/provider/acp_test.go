package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
	"github.com/ocman/ocman/internal/platforms/codex/acptest"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

func acpHarness(t *testing.T) (*harness, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("FAKE_CODEX", "1")
	t.Setenv("FAKE_CODEX_ACP_STATE", path)
	h := newHarness(t, func(s *codextest.Server) {
		scripted(s)
		acptest.ConfigureNative(s, path)
	}, Options{CLI: exe, ACPCommand: acpexec.Command{Executable: exe}, Home: t.TempDir(), MCPAddr: "127.0.0.1:42"})
	return h, path
}

func createACP(t *testing.T, h *harness) {
	t.Helper()
	var session platforms.CreateSessionResponse
	if code := h.call("POST", "/sessions", platforms.CreateSessionRequest{Directory: "/repo", Title: "Test"}, &session); code != http.StatusOK || session.ID != "thr" {
		t.Fatalf("create = %d %+v", code, session)
	}
}

func sendACP(t *testing.T, h *harness, req platforms.SendMessageRequest) {
	t.Helper()
	var response any
	if code := h.call("POST", "/sessions/thr/send", req, &response); code != http.StatusNoContent {
		t.Fatalf("send = %d %+v", code, response)
	}
}

func TestACPExecutionKeepsNativeIdentities(t *testing.T) {
	h, path := acpHarness(t)
	createACP(t, h)
	sendACP(t, h, platforms.SendMessageRequest{Message: "hi"})
	user := h.next("message.updated", func(p map[string]any) bool { return info(p)["role"] == "user" })
	if info(user)["id"] != "msg_u1" {
		t.Fatalf("user identity = %+v", user)
	}
	first := h.next("message.updated", func(p map[string]any) bool { return info(p)["role"] == "assistant" })
	if info(first)["id"] != "msg_r1" {
		t.Fatalf("empty native reasoning must own the reply group: %+v", first)
	}
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["id"] == "prt_a1" && part(p)["text"] == "Hello from ACP" })
	h.next("session.idle", nil)
	var detail platforms.SessionDetail
	if code := h.call("GET", "/sessions/thr", nil, &detail); code != http.StatusOK || len(detail.Messages) != 2 || detail.Messages[1].ID != "msg_r1" {
		t.Fatalf("native detail = %d %+v", code, detail)
	}
	state := acptest.ReadState(path)
	if state.CLI != h.b.opts.CLI || state.Home != h.b.opts.Home {
		t.Fatalf("adapter used a different native source: %+v", state)
	}
	if !slices.Contains(state.Calls, "session/new") || !slices.Contains(state.Calls, "session/prompt") {
		t.Fatal(state.Calls)
	}
	for _, call := range h.fake().Calls() {
		if slices.Contains([]string{codexappserver.MethodThreadResume, codexappserver.MethodThreadStart, codexappserver.MethodTurnStart}, call.Method) {
			t.Fatalf("native reader became an execution owner: %s", call.Method)
		}
	}
	// Another prompt uses the same ACP session and gets new canonical IDs.
	sendACP(t, h, platforms.SendMessageRequest{Message: "again", Model: "openai/gpt-test", Reasoning: "high"})
	h.next("session.idle", nil)
	state = acptest.ReadState(path)
	if len(state.Turns) != 2 || slices.Contains(state.Calls, "session/resume") || strings.Count(strings.Join(state.Calls, ","), "session/set_config_option") != 2 {
		t.Fatal(state.Calls)
	}
}

func TestACPPermissionResolutionIsDurable(t *testing.T) {
	t.Setenv("FAKE_CODEX_APPROVAL", "1")
	h, _ := acpHarness(t)
	createACP(t, h)
	sendACP(t, h, platforms.SendMessageRequest{Message: "hi"})
	request := h.next("permission.asked", nil)
	if request["permission"] != "bash" {
		t.Fatal(request)
	}
	id := request["id"].(string)
	if code := h.call("POST", "/permissions/"+id+"/reply", platforms.RespondPermissionRequest{Reply: "always"}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	h.next("permission.replied", nil)
	h.next("session.idle", nil)
	var pending []map[string]any
	h.call("GET", "/permissions?sessionID=thr", nil, &pending)
	if len(pending) != 0 {
		t.Fatal(pending)
	}
}

func TestACPQuestionsRetainSessionAndToolScope(t *testing.T) {
	t.Setenv("FAKE_CODEX_ACP_QUESTION", "1")
	h, _ := acpHarness(t)
	createACP(t, h)
	sendACP(t, h, platforms.SendMessageRequest{Message: "hi"})
	request := h.next("question.asked", nil)
	if request["sessionID"] != "thr" || request["tool"].(map[string]any)["callID"] != "question-tool" {
		t.Fatal(request)
	}
	id := request["id"].(string)
	if code := h.call("POST", "/questions/"+id+"/reply", platforms.RespondQuestionRequest{Answers: [][]string{{"A"}}}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	h.next("question.replied", nil)
	h.next("session.idle", nil)
}

func TestACPCancellationSteeringAndNativeReaderFailure(t *testing.T) {
	t.Setenv("FAKE_CODEX_ACP_HANG", "1")
	h, path := acpHarness(t)
	createACP(t, h)
	sendACP(t, h, platforms.SendMessageRequest{Message: "wait"})
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["id"] == "prt_a1" })
	// A read-only app-server failure must not release ACP's turn admission.
	h.b.mu.Lock()
	reader := h.b.client
	h.b.mu.Unlock()
	_ = reader.Close()
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	if !running {
		t.Fatal("native reader failure settled an ACP turn")
	}
	sendACP(t, h, platforms.SendMessageRequest{Message: "steer"})
	if code := h.call("POST", "/sessions/thr/abort", nil, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	h.next("session.idle", nil)
	state := acptest.ReadState(path)
	if !slices.Contains(state.Calls, "_session/steering") || !slices.Contains(state.Calls, "session/cancel") {
		t.Fatal(state.Calls)
	}
}

func TestACPDisconnectNeverFallsBack(t *testing.T) {
	t.Setenv("FAKE_CODEX_ACP_EXIT", "1")
	h, path := acpHarness(t)
	createACP(t, h)
	sendACP(t, h, platforms.SendMessageRequest{Message: "hi"})
	h.next("message.updated", func(p map[string]any) bool { return info(p)["error"] != nil })
	h.next("session.idle", nil)
	if count := strings.Count(strings.Join(acptest.ReadState(path).Calls, ","), "session/prompt"); count != 1 {
		t.Fatalf("prompt replayed %d times", count)
	}
	for _, call := range h.fake().Calls() {
		if call.Method == codexappserver.MethodTurnStart || call.Method == codexappserver.MethodThreadResume {
			t.Fatal("native fallback", call.Method)
		}
	}
}

func TestACPUnsupportedControlsAndMissingAdapter(t *testing.T) {
	h, _ := acpHarness(t)
	createACP(t, h)
	if code := h.call("POST", "/sessions/thr/shell", platforms.RunShellRequest{Command: "ls"}, nil); code != http.StatusNotImplemented {
		t.Fatal(code)
	}
	if code := h.call("POST", "/sessions/thr/send", platforms.SendMessageRequest{Message: "!ls"}, nil); code != http.StatusNotImplemented {
		t.Fatal("raw shell became an LLM prompt", code)
	}
	if code := h.call("POST", "/sessions/thr/fork", platforms.ForkSessionRequest{MessageID: "msg_u1"}, nil); code != http.StatusNotImplemented {
		t.Fatal(code)
	}
	if code := h.call("POST", "/sessions", platforms.CreateSessionRequest{Directory: "relative"}, nil); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	if code := h.call("POST", "/sessions/thr/send", platforms.SendMessageRequest{}, nil); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	if code := h.call("POST", "/sessions/thr/send", platforms.SendMessageRequest{Images: []platforms.ImageAttachment{{URL: "https://image"}}}, nil); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	if _, err := acpInput(platforms.SendMessageRequest{Images: []platforms.ImageAttachment{{URL: "data:image/png;base64,AA"}}}); err != nil {
		t.Fatal(err)
	}
	h.b.acp.close()
	h.b.opts.ACPCommand.Executable = filepath.Join(t.TempDir(), "missing")
	_, err := h.b.acp.create(context.Background(), platforms.CreateSessionRequest{Directory: "/repo"})
	if !errors.Is(err, platforms.ErrCreationRejected) || !strings.Contains(err.Error(), "install @agentclientprotocol/codex-acp") {
		t.Fatal(err)
	}
}

func TestACPQuestionSchemasFailClosed(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","properties":{"x":{"type":"string"}}}`,
		`{"type":"object","properties":{"x":{"type":"boolean"}}}`,
		`{"type":"object","properties":{"x":{"type":"string","enum":["a","b"]}}}`,
	} {
		var schema acp.UnstableElicitationSchema
		if err := json.Unmarshal([]byte(raw), &schema); err != nil {
			t.Fatal(err)
		}
		fields, err := acpQuestionFields(schema)
		if err != nil || len(fields) != 1 {
			t.Fatalf("%+v %v", fields, err)
		}
	}
	var secret acp.UnstableElicitationSchema
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"password":{"type":"string","_meta":{"codex":{"isSecret":true}}}}}`), &secret); err != nil {
		t.Fatal(err)
	}
	if _, err := acpQuestionFields(secret); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}
