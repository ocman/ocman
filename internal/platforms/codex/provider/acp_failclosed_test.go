package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func scopedTurn(h *harness) {
	h.b.acp.mu.Lock()
	h.b.acp.turns["thr"] = &acpTurn{}
	h.b.acp.mu.Unlock()
}

func TestACPPermissionOptionsAndCancellation(t *testing.T) {
	for _, kind := range []acp.ToolKind{acp.ToolKindEdit, acp.ToolKindOther} {
		t.Run(string(kind), func(t *testing.T) {
			h, _ := acpHarness(t)
			scopedTurn(h)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			req := acp.RequestPermissionRequest{SessionId: "thr", ToolCall: acp.ToolCallUpdate{ToolCallId: "opaque", Kind: &kind, Locations: []acp.ToolCallLocation{{Path: "/repo/x"}}},
				Options: []acp.PermissionOption{{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject"}}}
			result := make(chan acp.RequestPermissionResponse, 1)
			go func() { response, _ := h.b.acp.permission(ctx, req); result <- response }()
			asked := h.next("permission.asked", nil)
			id := asked["id"].(string)
			if kind == acp.ToolKindEdit && asked["patterns"].([]any)[0] != "/repo/x" {
				t.Fatal(asked)
			}
			h.b.mu.Lock()
			pr := h.b.prompts[id]
			h.b.mu.Unlock()
			if err := pr.deliver("invalid"); err == nil {
				t.Fatal("accepted invalid approval")
			}
			if err := h.b.replyPermission(id, "once"); !errors.Is(err, platforms.ErrUnsupported) {
				t.Fatalf("invented allow-once: %v", err)
			}
			if err := h.b.replyPermission(id, "bad"); err == nil {
				t.Fatal("invalid reply")
			}
			if kind == acp.ToolKindEdit {
				if err := h.b.replyPermission(id, "reject"); err != nil {
					t.Fatal(err)
				}
				if response := <-result; response.Outcome.Selected == nil || response.Outcome.Selected.OptionId != "reject" {
					t.Fatal(response)
				}
			} else {
				cancel()
				if response := <-result; response.Outcome.Cancelled == nil {
					t.Fatal(response)
				}
			}
		})
	}
	h, _ := acpHarness(t)
	response, err := h.b.acp.permission(t.Context(), acp.RequestPermissionRequest{SessionId: "unknown"})
	if err != nil || response.Outcome.Cancelled == nil {
		t.Fatalf("unowned permission: %+v %v", response, err)
	}
	scopedTurn(h)
	restore := breakSpool(t, h)
	response, err = h.b.acp.permission(t.Context(), acp.RequestPermissionRequest{SessionId: "thr", ToolCall: acp.ToolCallUpdate{ToolCallId: "x"}})
	restore()
	if err != nil || response.Outcome.Cancelled == nil {
		t.Fatal("unrecordable permission allowed")
	}
}

func formRequest(t *testing.T, schema string) acpexec.ElicitationRequest {
	t.Helper()
	var value acp.UnstableElicitationSchema
	if err := json.Unmarshal([]byte(schema), &value); err != nil {
		t.Fatal(err)
	}
	return acpexec.ElicitationRequest{SessionID: "thr", ToolCallID: "question", Form: &acp.UnstableCreateElicitationForm{Mode: "form", Message: "Question", RequestedSchema: value}}
}

func TestACPQuestionValidationAndTypedAnswers(t *testing.T) {
	h, _ := acpHarness(t)
	scopedTurn(h)
	req := formRequest(t, `{"type":"object","properties":{"a":{"type":"string","oneOf":[{"const":"one"}]},"b":{"type":"boolean"},"c":{"type":"string"},"d":{"type":"string"}}}`)
	result := make(chan acp.UnstableCreateElicitationResponse, 1)
	go func() { response, _ := h.b.acp.question(t.Context(), req); result <- response }()
	asked := h.next("question.asked", nil)
	id := asked["id"].(string)
	h.b.mu.Lock()
	pr := h.b.prompts[id]
	h.b.mu.Unlock()
	if err := pr.deliver("invalid"); err == nil {
		t.Fatal("invalid response accepted")
	}
	for _, answers := range [][][]string{{{"one", "two"}}, {{"not offered"}}} {
		if err := h.b.replyQuestion(id, answers); err == nil {
			t.Fatal("invalid answers accepted")
		}
	}
	pr.answering = true
	if err := h.b.replyQuestion(id, nil); err == nil {
		t.Fatal("concurrent answer accepted")
	}
	pr.answering = false
	if err := h.b.replyQuestion(id, [][]string{{"one"}, {"true"}, {"free text"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(<-result)
	var response struct {
		Content map[string]any `json:"content"`
	}
	_ = json.Unmarshal(raw, &response)
	if response.Content["b"] != true || response.Content["c"] != "free text" {
		t.Fatalf("typed answers lost: %s", raw)
	}
	go func() { response, _ := h.b.acp.question(t.Context(), req); result <- response }()
	id = h.next("question.asked", nil)["id"].(string)
	if err := h.b.rejectQuestion(id); err != nil {
		t.Fatal(err)
	}
	if response := <-result; response.Cancel == nil {
		t.Fatal("rejection accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() { response, _ := h.b.acp.question(ctx, req); result <- response }()
	h.next("question.asked", nil)
	cancel()
	if response := <-result; response.Cancel == nil {
		t.Fatal("cancelled question accepted")
	}
	if response, err := h.b.acp.question(t.Context(), acpexec.ElicitationRequest{}); err != nil || response.Cancel == nil {
		t.Fatal("unowned question accepted")
	}
	secret := formRequest(t, `{"type":"object","properties":{"secret":{"type":"number"}}}`)
	if _, err := h.b.acp.question(t.Context(), secret); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	restore := breakSpool(t, h)
	if response, err := h.b.acp.question(t.Context(), req); err != nil || response.Cancel == nil {
		t.Fatal("unrecordable question accepted")
	}
	restore()
}

func TestACPReaderFailuresAndNativeHistoryLag(t *testing.T) {
	h, _ := acpHarness(t)
	_, _, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	native := h.fake()
	native.Handle(codexappserver.MethodThreadItems, func(json.RawMessage) (any, error) { return nil, errors.New("items unavailable") })
	turn := &acpTurn{conv: &Converter{SessionID: "thr"}}
	if err := h.b.acp.seedUser(t.Context(), "thr", "a", turn); err == nil {
		t.Fatal("read error ignored")
	}
	native.Handle(codexappserver.MethodThreadItems, func(json.RawMessage) (any, error) {
		return nil, &codexappserver.RPCError{Code: -32600, Message: "no rollout"}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := h.b.acp.seedUser(ctx, "thr", "a", turn); err == nil {
		t.Fatal("history timeout ignored")
	}
	native.Handle(codexappserver.MethodThreadTurns, func(json.RawMessage) (any, error) { return nil, errors.New("turns unavailable") })
	if err := h.b.acp.reconcile(t.Context(), "thr", turn, false); err == nil {
		t.Fatal("turn read failed open")
	}
	native.Handle(codexappserver.MethodThreadTurns, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []codexappserver.Turn{{ID: "t", Status: "completed"}}}, nil
	})
	if err := h.b.acp.reconcile(t.Context(), "thr", turn, false); err == nil {
		t.Fatal("item read failed open")
	}
	for _, message := range []string{"not materialized", "no rollout", "thread not loaded", "invalid thread id"} {
		if !acpHistoryPending(&codexappserver.RPCError{Code: -32600, Message: message}) {
			t.Fatal(message)
		}
	}
	if acpHistoryPending(errors.New("other")) {
		t.Fatal("retried unrelated error")
	}
	for _, path := range []string{"/sessions", "/sessions/thr/send", "/sessions/thr/command", "/sessions/thr/fork", "/sessions/thr/rename", "/permissions/x/reply", "/questions/x/reply"} {
		if code := h.call("POST", path, "invalid object", nil); code != http.StatusBadRequest {
			t.Fatal(path, code)
		}
	}
	if code := h.call("POST", "/questions/x/unknown", nil, nil); code != http.StatusNotImplemented {
		t.Fatal(code)
	}
	native.Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) { return map[string]any{"data": []any{}}, nil })
	native.Handle(codexappserver.MethodSkillsList, func(json.RawMessage) (any, error) { return nil, errors.New("skills unavailable") })
	if code := h.call("GET", "/catalog?directory=/repo", nil, nil); code != 500 {
		t.Fatal(code)
	}
}

func TestACPAdmissionAndConfigurationFailures(t *testing.T) {
	h, _ := acpHarness(t)
	createACP(t, h)
	h.b.mu.Lock()
	session := h.b.sessionLocked("thr")
	h.b.mu.Unlock()
	session.sendMu.Lock()
	if err := h.b.acp.send(t.Context(), "thr", platforms.SendMessageRequest{Message: "hi"}); !errors.Is(err, platforms.ErrBusy) {
		t.Fatal(err)
	}
	session.sendMu.Unlock()
	c, err := h.b.acp.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c.Info().Meta["steering"] = map[string]any{"supported": false}
	if err := h.b.acp.steer(t.Context(), c, "thr", []acp.ContentBlock{acp.TextBlock("hi")}); !errors.Is(err, platforms.ErrBusy) {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.fail = errors.New("reader unavailable")
	h.mu.Unlock()
	if err := h.b.acp.configure(t.Context(), c, "thr", platforms.SendMessageRequest{Model: "model"}); err == nil {
		t.Fatal("unavailable reader ignored")
	}
	if _, _, err := h.b.conn(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.fake().Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) { return nil, errors.New("models unavailable") })
	if err := h.b.acp.configure(t.Context(), c, "thr", platforms.SendMessageRequest{Model: "model"}); err == nil {
		t.Fatal("unavailable models ignored")
	}
	if err := h.b.acp.rename(t.Context(), "thr", "Name"); err != nil {
		t.Fatal(err)
	}
	h.fake().Handle(codexappserver.MethodThreadNameSet, func(json.RawMessage) (any, error) { return nil, errors.New("name unavailable") })
	if err := h.b.acp.rename(t.Context(), "thr", "Name"); err == nil {
		t.Fatal("title failure ignored")
	}
	_ = c.Close()
	if err := h.b.acp.configure(t.Context(), c, "thr", platforms.SendMessageRequest{Reasoning: "high"}); err == nil {
		t.Fatal("dead adapter accepted reasoning")
	}
}

func TestACPStoppedSupervisorRejectsEveryNewOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b := &backend{ctx: ctx}
	a := newACPExecution(b)
	if _, err := a.conn(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := a.create(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"}); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatal(err)
	}
	if _, err := a.fork(t.Context(), "thr", ""); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatal(err)
	}
	a.close()
	h, _ := acpHarness(t)
	createACP(t, h)
	if err := h.b.acp.resume(t.Context(), nil, "thr"); err == nil {
		t.Fatal("stale connection resumed thread")
	}
	if got := h.b.snapshot("unknown"); len(got) != 0 {
		t.Fatal(got)
	}
	if code := h.call("POST", "/questions/missing/reject", nil, nil); code != 404 {
		t.Fatal(code)
	}
	h.b.cancelPrompt("missing")
	h.start("thr")
	h.b.acp.settle("thr", &acpTurn{admission: 1, failed: true}, nil, false)
	h.next("session.idle", nil)
	h.b.mu.Lock()
	h.b.sessions["thr"].running = true
	h.b.sessions["thr"].accepted = 2
	h.b.mu.Unlock()
	h.b.acp.settle("thr", &acpTurn{admission: 2, cancelled: true}, nil, false)
	h.next("session.idle", nil)
	for _, path := range []string{"/permissions?sessionID=none", "/questions?sessionID=none", "/sessions?runtime=1&directory=/other"} {
		if code := h.call("GET", path, nil, nil); code != 200 {
			t.Fatal(path, code)
		}
	}
	h.b.mu.Lock()
	reader := h.b.client
	h.b.mu.Unlock()
	if reader != nil {
		_ = reader.Close()
	}
	h.mu.Lock()
	h.fail = errors.New("reader unavailable")
	h.mu.Unlock()
	if err := h.b.acp.rename(t.Context(), "thr", "Name"); err == nil {
		t.Fatal("reader failure ignored")
	}
}
