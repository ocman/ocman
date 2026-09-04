package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
)

// The dedicated MCP listener is the credential-free path for local MCP
// clients: it must serve them even when password auth is configured,
// because a native MCP client has no way to send an auth cookie.
func TestMCPListenerServesLocalClientWithPasswordAuth(t *testing.T) {
	srv := newWorkflowTestServer(t)
	srv.auth = newTestAuth(t, "hunter2")
	srv.mcpAddr = "127.0.0.1:0"
	defer srv.startMCPListener()()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.mcpAddr+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post /mcp: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), `"tools"`) {
		t.Fatalf("want a tools/list result, got: %s", payload)
	}
}

// Refuse to serve MCP on a non-loopback address: the endpoint accepts
// the peer address as its credential, so it must not be reachable off
// the machine. Failing closed leaves /mcp on the main port under auth.
func TestMCPListenerRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "192.0.2.1:8227", "not-an-addr"} {
		srv := newWorkflowTestServer(t)
		srv.mcpAddr = addr
		stop := srv.startMCPListener()
		stop()
		if srv.mcpAddr != "" {
			t.Errorf("%s: unavailable listener remained advertised as %q", addr, srv.mcpAddr)
		}
	}
}

func TestMCPListenerDisabledWhenAddrEmpty(t *testing.T) {
	srv := newWorkflowTestServer(t)
	srv.startMCPListener()()
	if srv.mcpAddr != "" {
		t.Fatalf("mcpAddr changed to %q", srv.mcpAddr)
	}
}

func TestMCPServerURL(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		mcpAddr string
		want    string
	}{
		{"dedicated listener wins", "127.0.0.1:8228", "127.0.0.1:8227", "http://127.0.0.1:8227/mcp"},
		{"falls back to main addr", "127.0.0.1:8228", "", "http://127.0.0.1:8228/mcp"},
		{"bare port gets a host", "", ":9000", "http://localhost:9000/mcp"},
		{"no addr at all", "", "", "http://localhost:8229/mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{addr: tt.addr, mcpAddr: tt.mcpAddr}
			if got := s.mcpServerURL(); got != tt.want {
				t.Errorf("mcpServerURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMainMuxMCPAllowsGraphEdits(t *testing.T) {
	svc := &fakeFactoryService{}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"factory","arguments":{"action":"mutate_graph","mutation_json":"{\"epicId\":\"epic\",\"action\":\"edit\",\"issueId\":\"i\",\"title\":\"t\"}"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "factory action is not permitted") {
		t.Fatalf("main-mux /mcp rejected graph edit: %s", rec.Body.String())
	}
	if svc.mutation.Action != "edit" || svc.mutation.IssueID != "i" || svc.mutation.Actor != "mcp" {
		t.Fatalf("MutateGraph = %+v", svc.mutation)
	}
}

func TestMainMuxMCPRejectsExecutableIssueCreation(t *testing.T) {
	svc := &fakeFactoryService{}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"factory","arguments":{"action":"mutate_graph","mutation_json":"{\"epicId\":\"epic\",\"action\":\"create\",\"parentId\":\"epic.1\",\"kind\":\"task\",\"title\":\"Work\"}"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "factory action is not permitted") {
		t.Fatalf("main-mux /mcp accepted executable issue creation: %s", rec.Body.String())
	}
	if svc.mutation.Action != "" {
		t.Fatalf("MutateGraph = %+v, want no call", svc.mutation)
	}
}

func TestDedicatedMCPFactoryServiceRejectsUserOnlyActions(t *testing.T) {
	service := factoryMCPService{&fakeFactoryService{epics: []factory.WorkEpic{{ID: "epic"}}}}
	checks := []struct {
		name string
		call func() error
	}{
		{"create Epic", func() error {
			_, err := service.CreateWorkEpic(t.Context(), factory.CreateWorkEpicRequest{})
			return err
		}},
		{"save Formula", func() error { _, err := service.SaveFormula(t.Context(), factory.FormulaSaveRequest{}); return err }},
		{"set capacity", func() error { _, err := service.SetCapacityPolicy(t.Context(), factory.CapacityPolicy{}); return err }},
		{"decide Plan", func() error {
			_, err := service.DecidePlanGate(t.Context(), "epic", "approve", factory.PlanGateDecisionRequest{})
			return err
		}},
		{"resolve recovery", func() error { _, err := service.ResolveRecoveryGate(t.Context(), "gate", "resume", ""); return err }},
		{"resolve authority", func() error {
			_, err := service.ResolveAuthorityEscalationGate(t.Context(), "gate", "approve")
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, factory.ErrActionNotPermitted) {
				t.Fatalf("error = %v, want ErrActionNotPermitted", err)
			}
		})
	}
}

func TestDedicatedMCPFactoryServiceAllowsGraphMutations(t *testing.T) {
	underlying := &fakeFactoryService{}
	service := factoryMCPService{underlying}
	for _, action := range []string{"create", "edit", "reparent", "link", "unlink", "delete"} {
		mutation := factory.GraphMutation{Action: action, EpicID: "epic", ParentID: "epic.1", Kind: "mol", Title: "Work"}
		if err := service.MutateGraph(t.Context(), mutation); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if underlying.mutation != mutation {
			t.Fatalf("mutation = %+v, want %+v", underlying.mutation, mutation)
		}
	}
	for _, kind := range []string{"implementation", "task"} {
		if err := service.MutateGraph(t.Context(), factory.GraphMutation{Action: "create", Kind: kind}); !errors.Is(err, factory.ErrActionNotPermitted) {
			t.Fatalf("%s create error = %v, want ErrActionNotPermitted", kind, err)
		}
	}
}
