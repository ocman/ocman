package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

// The runner posts every non-command node here, so the shim endpoint has
// to reject malformed callbacks before it touches run state.
func TestHandleWorkflowStepRejectsBadRequests(t *testing.T) {
	t.Run("no state database", func(t *testing.T) {
		rec := httptest.NewRecorder()
		(&Server{}).handleWorkflowStep(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-step", strings.NewReader(`{}`)))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("code = %d, want 503", rec.Code)
		}
	})

	srv := newWorkflowTestServer(t)
	for name, tc := range map[string]struct{ body, want string }{
		"invalid json": {`{`, "invalid JSON body"},
		"no node":      {`{"runId":"r1"}`, "nodeId is required"},
		"no run":       {`{"nodeId":"n1"}`, "runId is required"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.handleWorkflowStep(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-step", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

// A map-node step carries only parentRunId, so the run must resolve from
// that fallback rather than being rejected as missing.
func TestHandleWorkflowStepFallsBackToParentRun(t *testing.T) {
	srv, run := workflowStepRun(t, nil)
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"kind":"condition","parentRunId":%q,"nodeId":"ship","from":"review"}`, run.ID)
	srv.handleWorkflowStep(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-step", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp workflowStepResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Error != "" {
		t.Fatalf("resp = %+v", resp)
	}
}

// A step failure travels back as a JSON error, not an HTTP status: the
// shim turns it into a non-zero exit and the runner records the node.
func TestHandleWorkflowStepReportsUnsupportedKind(t *testing.T) {
	srv, run := workflowStepRun(t, nil)
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"kind":"telepathy","runId":%q,"nodeId":"review"}`, run.ID)
	srv.handleWorkflowStep(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-step", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var resp workflowStepResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Error, "unsupported workflow step kind") {
		t.Fatalf("resp = %+v", resp)
	}
}

// The runner only sends ids, so a step that names a run or node the
// pinned version does not contain must fail loudly.
func TestWorkflowStepNodeRejectsUnknownIDs(t *testing.T) {
	srv, run := workflowStepRun(t, nil)
	if _, err := srv.workflowStepNode(t.Context(), "missing-run", "review"); err == nil {
		t.Fatal("unknown run: want error")
	}
	if _, err := srv.workflowStepNode(t.Context(), run.ID, "nowhere"); err == nil {
		t.Fatal("unknown node: want error")
	}
	node, err := srv.workflowStepNode(t.Context(), run.ID, "ship")
	if err != nil || node.ID != "ship" {
		t.Fatalf("node = %+v err = %v", node, err)
	}
}

func TestEvaluateWorkflowCondition(t *testing.T) {
	definition := `{"id":"gated","name":"Gated","version":"1","concurrency":1,
		"triggers":[{"id":"manual","type":"manual"}],
		"nodes":[{"id":"review","name":"Review","type":"approval"},{"id":"ship","name":"Ship","type":"approval"}],
		"dependencies":[{"from":"review","to":"ship","condition":"outcomes[\"review\"].output.ok == true"}]}`
	srv, run := workflowStepRun(t, &definition)

	for name, tc := range map[string]struct {
		upstream string
		wantOK   bool
	}{
		"condition true":  {`{"review":{"ok":true}}`, true},
		"condition false": {`{"review":{"ok":false}}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := srv.evaluateWorkflowCondition(t.Context(), run.ID, workflowStepRequest{
				NodeID: "ship", From: "review", Upstream: json.RawMessage(tc.upstream),
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.OK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", resp.OK, tc.wantOK)
			}
		})
	}

	// An edge the author never gated must let the step through instead of
	// skipping it.
	resp, err := srv.evaluateWorkflowCondition(t.Context(), run.ID, workflowStepRequest{NodeID: "ship", From: "elsewhere"})
	if err != nil || !resp.OK {
		t.Fatalf("ungated edge: resp = %+v err = %v", resp, err)
	}
	if _, err := srv.evaluateWorkflowCondition(t.Context(), "missing-run", workflowStepRequest{NodeID: "ship"}); err == nil {
		t.Fatal("unknown run: want error")
	}
}

// The condition environment only exposes upstream outputs, and a
// reference to a node that produced nothing must surface as an
// evaluation error rather than a silent false.
func TestEvaluateWorkflowConditionSurfacesExpressionErrors(t *testing.T) {
	definition := `{"id":"broken","name":"Broken","version":"1","concurrency":1,
		"triggers":[{"id":"manual","type":"manual"}],
		"nodes":[{"id":"review","name":"Review","type":"approval"},{"id":"ship","name":"Ship","type":"approval"}],
		"dependencies":[{"from":"review","to":"ship","condition":"outcomes[\"absent\"].output.ok == true"}]}`
	srv, run := workflowStepRun(t, &definition)
	if _, err := srv.evaluateWorkflowCondition(t.Context(), run.ID, workflowStepRequest{
		NodeID: "ship", From: "review", Upstream: json.RawMessage(`{"review":{"ok":true}}`),
	}); err == nil {
		t.Fatal("want evaluation error for an unknown outcome reference")
	}
}

func TestAwaitWorkflowApproval(t *testing.T) {
	restore := workflowStepPollInterval
	workflowStepPollInterval = time.Millisecond
	t.Cleanup(func() { workflowStepPollInterval = restore })

	t.Run("granted", func(t *testing.T) {
		srv, run := workflowStepRun(t, nil)
		if _, err := srv.workflowSvc().Approve(t.Context(), run.ID, "review"); err != nil {
			t.Fatal(err)
		}
		resp, err := srv.awaitWorkflowApproval(t.Context(), run.ID, "review")
		if err != nil || !resp.OK {
			t.Fatalf("resp = %+v err = %v", resp, err)
		}
	})

	t.Run("run canceled", func(t *testing.T) {
		srv, run := workflowStepRun(t, nil)
		if _, err := srv.workflowSvc().Cancel(t.Context(), run.ID); err != nil {
			t.Fatal(err)
		}
		resp, err := srv.awaitWorkflowApproval(t.Context(), run.ID, "review")
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK || resp.Error != "approval was not granted" {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("caller gives up", func(t *testing.T) {
		srv, run := workflowStepRun(t, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := srv.awaitWorkflowApproval(ctx, run.ID, "review"); err == nil {
			t.Fatal("want context error")
		}
	})

	t.Run("unknown run", func(t *testing.T) {
		srv := newWorkflowTestServer(t)
		if _, err := srv.awaitWorkflowApproval(t.Context(), "missing-run", "review"); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestRunWorkflowAgentStep(t *testing.T) {
	restore := workflowStepPollInterval
	workflowStepPollInterval = time.Millisecond
	t.Cleanup(func() { workflowStepPollInterval = restore })

	t.Run("missing configuration", func(t *testing.T) {
		srv := newWorkflowTestServer(t)
		if _, err := srv.runWorkflowAgentStep(t.Context(), workflows.Node{ID: "agent"}, workflowStepRequest{}); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("completes with the final message as output", func(t *testing.T) {
		srv, node := agentStepServer(t, "idle", `{"status":"done"}`, nil)
		resp, err := srv.runWorkflowAgentStep(t.Context(), node, workflowStepRequest{
			Upstream: json.RawMessage(`{"build":{"tag":"v9"}}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !resp.OK || string(resp.Output) != `{"status":"done"}` {
			t.Fatalf("resp = %+v output = %s", resp, resp.Output)
		}
	})

	t.Run("session error", func(t *testing.T) {
		srv, node := agentStepServer(t, "error", "", nil)
		resp, err := srv.runWorkflowAgentStep(t.Context(), node, workflowStepRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK || resp.Error != "boom" {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("send failure reports before polling", func(t *testing.T) {
		srv, node := agentStepServer(t, "idle", "", func(platforms.SendMessageRequest) error {
			return fmt.Errorf("no route to session")
		})
		resp, err := srv.runWorkflowAgentStep(t.Context(), node, workflowStepRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK || !strings.Contains(resp.Error, "no route to session") {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("caller gives up mid-turn", func(t *testing.T) {
		srv, node := agentStepServer(t, "busy", "", nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := srv.runWorkflowAgentStep(ctx, node, workflowStepRequest{}); err == nil {
			t.Fatal("want context error")
		}
	})
}

// selectJSONPath backs every ${...} reference a step prompt can carry, so
// a selector that walks off the payload must yield empty rather than
// panicking or leaking Go syntax into a prompt.
func TestSelectJSONPathHandlesMisses(t *testing.T) {
	for name, tc := range map[string]struct {
		payload, selector, want string
	}{
		"whole object":     {`{"a":{"b":"c"}}`, "", `{"a":{"b":"c"}}`},
		"nested string":    {`{"a":{"b":"c"}}`, "a.b", "c"},
		"nested object":    {`{"a":{"b":"c"}}`, "a", `{"b":"c"}`},
		"through a scalar": {`{"a":1}`, "a.b", ""},
		"absent key":       {`{"a":1}`, "zzz", ""},
		"invalid payload":  {`{`, "a", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := selectJSONPath(json.RawMessage(tc.payload), tc.selector); got != tc.want {
				t.Fatalf("selectJSONPath = %q, want %q", got, tc.want)
			}
		})
	}
}

// An unterminated reference must be left alone instead of looping.
func TestSubstituteJSONPathsLeavesUnclosedReferences(t *testing.T) {
	got := substituteJSONPaths("start ${item.path", "${item", json.RawMessage(`{"path":"x"}`))
	if got != "start ${item.path" {
		t.Fatalf("got %q", got)
	}
}

// workflowStepRun publishes a definition and starts a run for it,
// returning the server the shim handlers hang off.
func workflowStepRun(t *testing.T, definition *string) (*Server, workflows.RunDetail) {
	t.Helper()
	srv := newWorkflowTestServer(t)
	body := workflowRequest
	if definition != nil {
		body = *definition
	}
	version, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.workflowSvc().Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	return srv, run
}

// agentStepServer wires a fake platform whose session settles in the
// given status, and returns the agent node pointing at it.
func agentStepServer(t *testing.T, status db.SessionStatus, message string, send func(platforms.SendMessageRequest) error) (*Server, workflows.Node) {
	t.Helper()
	dir := t.TempDir()
	p := &fakePlatform{
		id: "fake",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "agent-session"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			if send == nil {
				return nil
			}
			return send(req)
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session: &db.Session{
					ID: id, Platform: "fake", Directory: dir,
					Status: status, LastErrorMessage: "boom",
				},
				Messages: []db.Message{{ID: "assistant", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant"}`)}},
				Parts: []db.Part{{MessageID: "assistant", Data: json.RawMessage(
					`{"type":"text","text":` + mustJSONString(message) + `}`)}},
			}, nil
		},
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openTestStateDB(t), "", registry, nil)
	node := workflows.Node{ID: "agent", Type: "agent", Agent: &workflows.AgentConfig{
		Platform: "fake", Directory: dir, Prompt: "ship ${nodes.build.output.tag}",
	}}
	return srv, node
}

func mustJSONString(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
