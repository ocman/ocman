package pluginsmod

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func toolModule(t *testing.T) (*Module, *[]plugins.SessionToolCall, *[]string) {
	t.Helper()
	m := NewModule(Deps{})
	ctx, cancel := context.WithCancel(t.Context())
	m.Bind(ctx)
	t.Cleanup(func() {
		m.sessionTools.Range(func(_, value any) bool { m.closeSessionToolBinding(value.(*sessionToolBinding)); return true })
		cancel()
		m.workers.Close()
	})
	calls := &[]plugins.SessionToolCall{}
	grants := &[]string{plugins.SessionRunGrant, plugins.SessionPluginGrant, plugins.SessionToolsGrant}
	d := plugins.Description{Capabilities: []plugins.Capability{plugins.ToolSessionCapability}, RequestedGrants: append([]string{}, (*grants)...)}
	m.sessionOnce.Do(func() {})
	var mu sync.Mutex
	m.sessionBroker = plugins.NewSessionBroker(func(_ context.Context, _ string, use func(plugins.Description, []string, plugins.Config) error) error {
		return use(d, *grants, plugins.Config{})
	}, nil,
		func(_ context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
			var p plugins.SessionToolCall
			if json.Unmarshal(call.Params, &p) != nil {
				t.Fatal("invalid callback")
			}
			mu.Lock()
			*calls = append(*calls, p)
			mu.Unlock()
			replies := make(chan plugins.Reply, 1)
			replies <- plugins.Reply{Message: plugins.Envelope{Type: plugins.TypeResult, Result: &plugins.Result{Value: json.RawMessage(`{"ok":true}`)}}}
			close(replies)
			return replies, nil
		})
	return m, calls, grants
}

func openTools(t *testing.T, m *Module, plugin, session, contextID, name string, maxCalls int) *sessionToolBinding {
	t.Helper()
	prompt, err := m.openSessionTools(plugin, "platform", session, &plugins.SessionTools{Context: contextID, MaxCalls: maxCalls, TimeoutSeconds: 60,
		Tools: []plugins.SessionTool{{Name: name, Description: "Read a fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	var binding *sessionToolBinding
	m.sessionTools.Range(func(_, v any) bool {
		b := v.(*sessionToolBinding)
		if b.session == session {
			binding = b
		}
		return true
	})
	if binding == nil || !strings.Contains(prompt, binding.token) || !strings.Contains(prompt, name) {
		t.Fatal("missing private tool instructions")
	}
	return binding
}

func TestSessionToolBindingsIsolateUnrelatedWorkflows(t *testing.T) {
	m, calls, grants := toolModule(t)
	doc := openTools(t, m, "documents", "session-doc", "doc-run", "read-document", 3)
	stock := openTools(t, m, "inventory", "session-stock", "stock-run", "lookup-item", 2)
	if _, err := m.callSessionTool(t.Context(), doc.token, "lookup-item", json.RawMessage(`{}`)); err == nil {
		t.Fatal("foreign tool admitted")
	}
	for _, b := range []*sessionToolBinding{doc, stock} {
		if _, err := m.callSessionTool(t.Context(), b.token, b.params.Tools[0].Name, json.RawMessage(`{"id":"fixture"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(*calls) != 2 || (*calls)[0].Context != "doc-run" || (*calls)[1].SessionID != "session-stock" {
		t.Fatalf("routing: %+v", *calls)
	}
	*grants = []string{plugins.SessionRunGrant, plugins.SessionPluginGrant}
	if _, err := m.callSessionTool(t.Context(), stock.token, "lookup-item", json.RawMessage(`{}`)); err == nil {
		t.Fatal("revocation ignored")
	}
	if _, err := m.callSessionTool(t.Context(), "unknown", "read-document", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown token admitted")
	}
}

func TestSessionToolsBoundCallsAndRevokeOnCompletionOrStop(t *testing.T) {
	m, calls, _ := toolModule(t)
	b := openTools(t, m, "documents", "session-a", "run-a", "read-document", 1)
	if _, err := m.callSessionTool(t.Context(), b.token, "read-document", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.callSessionTool(t.Context(), b.token, "read-document", json.RawMessage(`{}`)); err == nil || len(*calls) != 1 {
		t.Fatal("call limit exceeded")
	}
	m.revokeSessionTools("platform", "session-a")
	if _, ok := m.sessionTools.Load(b.token); ok {
		t.Fatal("completed binding retained")
	}
	b = openTools(t, m, "documents", "session-b", "run-b", "read-document", 2)
	_ = m.stopSessionTools(t.Context(), "other-plugin", "run-b")
	if _, ok := m.sessionTools.Load(b.token); !ok {
		t.Fatal("another plugin stopped binding")
	}
	_ = m.stopSessionTools(t.Context(), "documents", "run-b")
	if _, err := m.callSessionTool(t.Context(), b.token, "read-document", json.RawMessage(`{}`)); err == nil {
		t.Fatal("stopped binding admitted")
	}
}

func TestSessionToolMCPAndPermissionPolicy(t *testing.T) {
	m, _, _ := toolModule(t)
	b := openTools(t, m, "documents", "session-a", "run-a", "read-document", 2)
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"binding": b.token, "tool": "read-document", "arguments": map[string]any{}}
	result, err := m.handleSessionTool(t.Context(), req)
	if err != nil || result.IsError {
		t.Fatalf("MCP callback: %+v %v", result, err)
	}
	req.Params.Arguments = map[string]any{"binding": b.token, "arguments": "bad"}
	result, _ = m.handleSessionTool(t.Context(), req)
	if !result.IsError {
		t.Fatal("non-object admitted")
	}
	rules := toolSessionRules()
	if len(rules) != 3 || rules[0].Action != "deny" || rules[0].Permission != "*" {
		t.Fatal(rules)
	}
	for _, rule := range rules[1:] {
		if !strings.Contains(rule.Permission, "plugin_session_tool") || rule.Action != "allow" {
			t.Fatal(rule)
		}
	}
}

func TestSessionToolLifetimeCancelsInFlightWork(t *testing.T) {
	m, _, _ := toolModule(t)
	p := &plugins.SessionTools{Context: "run-a", MaxCalls: 1, TimeoutSeconds: 1, Tools: []plugins.SessionTool{{Name: "lookup", Description: "Lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	if _, err := m.openSessionTools("inventory", "platform", "session-a", p); err != nil {
		t.Fatal(err)
	}
	var b *sessionToolBinding
	m.sessionTools.Range(func(_, v any) bool { b = v.(*sessionToolBinding); return false })
	select {
	case <-b.done:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not expire")
	}
	if _, ok := m.sessionTools.Load(b.token); ok {
		t.Fatal("expired binding retained")
	}
}
