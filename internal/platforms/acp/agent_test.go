package acpexec

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// TestACPAgentProcess is a disposable stdio peer, never a real installed agent.
func TestACPAgentProcess(t *testing.T) {
	if os.Getenv("OCMAN_ACP_TEST_PROCESS") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "exit" {
		os.Exit(1)
	}
	if mode == "hang-initialize" {
		_, _ = bufio.NewReader(os.Stdin).ReadString('\x00')
		os.Exit(0)
	}
	encoder := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if err := encoder.Encode(v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	var pending json.RawMessage
	update := func(text string) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": acp.SessionNotification{
			SessionId: "native-session", Update: acp.UpdateAgentMessageText(text),
		}})
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params json.RawMessage   `json:"params"`
			Result json.RawMessage   `json:"result"`
			Error  *acp.RequestError `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			os.Exit(1)
		}
		reply := func(value any) { send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": value}) }
		rpcError := func(code int, message string) {
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": code, "message": message}})
		}
		switch m.Method {
		case "initialize":
			var request acp.InitializeRequest
			_ = json.Unmarshal(m.Params, &request)
			if request.ClientInfo == nil || request.ClientInfo.Name != "ocman" || request.ProtocolVersion != 1 ||
				request.ClientCapabilities.Terminal || request.ClientCapabilities.Fs.ReadTextFile || request.ClientCapabilities.Fs.WriteTextFile {
				rpcError(-32602, "unexpected client capabilities")
				continue
			}
			if mode == "initialize-error" {
				rpcError(-32603, "initialize failed")
				continue
			}
			version := 1
			if mode == "wrong-version" {
				version = 2
			}
			caps := map[string]any{}
			if mode != "no-capabilities" {
				caps["loadSession"] = true
				caps["sessionCapabilities"] = map[string]any{"resume": map[string]any{}, "fork": map[string]any{}}
			}
			reply(map[string]any{"protocolVersion": version, "agentInfo": map[string]any{"name": "fake", "version": "test"},
				"agentCapabilities": caps, "authMethods": []any{map[string]any{"id": "test-login", "name": "Test login"}}})
		case "authenticate":
			reply(map[string]any{})
		case "session/set_config_option", "_session/test":
			reply(map[string]any{})
		case "session/fork":
			reply(map[string]any{"sessionId": "forked"})
		case "session/new", "session/load", "session/resume":
			var params struct {
				Cwd        string `json:"cwd"`
				McpServers []any  `json:"mcpServers"`
			}
			_ = json.Unmarshal(m.Params, &params)
			if !filepath.IsAbs(params.Cwd) || (m.Method != "session/resume" && params.McpServers == nil) {
				rpcError(-32602, "missing absolute cwd or MCP array")
				continue
			}
			if m.Method == "session/load" {
				update("history")
			}
			reply(map[string]any{"sessionId": "native-session"})
		case "session/prompt":
			var p acp.PromptRequest
			_ = json.Unmarshal(m.Params, &p)
			text := p.Prompt[0].Text.Text
			switch text {
			case "auth-error":
				rpcError(-32000, "authentication required")
			case "disconnect":
				os.Exit(0)
			case "wait":
				pending = m.ID
				update("started")
			case "permission":
				pending = m.ID
				send(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{
					"sessionId": "native-session", "toolCall": map[string]any{"toolCallId": "tool-1", "title": "Edit"},
					"options": []any{map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"}},
				}})
			default:
				update(text)
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": acp.SessionNotification{
					SessionId: p.SessionId, Update: acp.StartToolCall("tool-1", "Read", acp.WithStartStatus(acp.ToolCallStatusCompleted)),
				}})
				reply(map[string]any{"stopReason": "end_turn"})
			}
		case "session/cancel":
			send(map[string]any{"jsonrpc": "2.0", "id": pending, "result": map[string]any{"stopReason": "cancelled"}})
		case "$/cancel_request":
			// session/cancel settles the prompt below.
		case "":
			// Forward the permission outcome for assertions by the parent test.
			if m.Error != nil {
				send(map[string]any{"jsonrpc": "2.0", "id": pending, "error": m.Error})
			} else {
				var result acp.RequestPermissionResponse
				_ = json.Unmarshal(m.Result, &result)
				stop := "cancelled"
				if result.Outcome.Selected != nil {
					stop = "end_turn"
				}
				send(map[string]any{"jsonrpc": "2.0", "id": pending, "result": map[string]any{"stopReason": stop}})
			}
		default:
			rpcError(-32601, "unknown method")
		}
	}
	if mode == "ignore-eof" {
		// Remain alive without holding any shared resources until Close kills us.
		_, _ = os.Stderr.WriteString("ignoring EOF\n")
		for { // a timer prevents the runtime deadlock detector from exiting us
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}
