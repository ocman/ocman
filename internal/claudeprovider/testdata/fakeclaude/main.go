// Command fakeclaude stands in for the claude CLI in tests (NFR-6). It replays
// a recorded stream-json fixture from testdata/claudecode/<version>/: stdout
// rows are written, and each stdin row waits for one input line of the same
// message type. A control_response must answer an open can_use_tool request
// with the recorded request_id and a well-formed decision, or the fake exits
// with status 3. A recorded control_request row naming a request.subtype
// only accepts that subtype. Every stdin line must be a well-formed user message or
// control request (checkInput); a control request with an unknown subtype is
// answered with the CLI's error response, as 2.1.220 does. It never contacts
// Anthropic or reads credentials.
//
// Environment:
//
//	FAKE_CLAUDE_FIXTURE  fixture to replay (required unless --version)
//	FAKE_CLAUDE_ARGS     optional file receiving the argv as JSON
//	FAKE_CLAUDE_VERSION  optional version reported by --version
//	FAKE_CLAUDE_STDIN    optional file receiving every stdin line
//	FAKE_CLAUDE_PRINT    the judge's one-shot answer: a call with
//	                     --no-session-persistence reads its prompt from stdin,
//	                     prints this text and exits without a fixture
//	FAKE_CLAUDE_PRINT_LOG optional file receiving that call's argv and prompt
//	FAKE_CLAUDE_PLUGIN_LOG optional file receiving each `plugin` call's argv;
//	                     install/uninstall toggle enabledPlugins in
//	                     $CLAUDE_CONFIG_DIR/settings.json like the real CLI
//	FAKE_CLAUDE_PLUGIN_FAIL `plugin` subcommand (e.g. "install") that fails
//	FAKE_CLAUDE_CWD_LOG  optional file each replaying run appends its cwd to
//	FAKE_CLAUDE_HOLD     optional path: the result row waits until it exists,
//	                     holding the turn open mid-stream
//	FAKE_CLAUDE_EXIT_DELAY optional duration a replaying run lingers after its
//	                     stdin closes, like a slow-exiting CLI
//	FAKE_CLAUDE_ENV_LOG  optional file each replaying run appends its
//	                     CLAUDE_CONFIG_DIR to ("<unset>" when absent)
//	FAKE_CLAUDE_LIFE_LOG optional file each replaying run appends
//	                     "start <unix ns>" and "exit <unix ns>" lines to
//	FAKE_CLAUDE_MCP_LOG  optional file: on start, each replaying run sends an
//	                     MCP initialize request to every http server in its
//	                     --mcp-config, as the real CLI connects, and appends
//	                     "<url> ok" or "<url> failed: <reason>"
//	FAKE_CLAUDE_VALIDATE_RESUME when set, sessions live in the active
//	                     configuration's history like the real CLI's: a
//	                     --session-id run records the id under
//	                     $CLAUDE_CONFIG_DIR, and --resume of an id that
//	                     configuration never recorded exits 1 with the CLI's
//	                     "No conversation found" error
//
// Synthetic fixtures may also use {"direction":"wait","message":"<path>"},
// which blocks until the file exists, and {"direction":"exit","message":<code>},
// which exits mid-turn like a crashed CLI. A stdout row's "uuid":"$input:N" is
// replaced by the uuid of the Nth stdin user message, as --replay-user-messages
// echoes it.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const version = "2.1.220"

type envelope struct {
	Direction string          `json:"direction"`
	Message   json.RawMessage `json:"message"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		plugin(os.Args[2:])
		return
	}
	if path := os.Getenv("FAKE_CLAUDE_ARGS"); path != "" && !contains(os.Args[1:], "--no-session-persistence") {
		data, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(path, data, 0o600); err != nil {
			fail(1, "write args: %v", err)
		}
	}
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			v := version
			if override := os.Getenv("FAKE_CLAUDE_VERSION"); override != "" {
				v = override
			}
			fmt.Println(v + " (Claude Code)")
			return
		}
	}
	if contains(os.Args[1:], "--no-session-persistence") {
		prompt, _ := io.ReadAll(os.Stdin)
		if path := os.Getenv("FAKE_CLAUDE_PRINT_LOG"); path != "" {
			data, _ := json.Marshal(map[string]any{"args": os.Args[1:], "prompt": string(prompt)})
			_ = os.WriteFile(path, data, 0o600)
		}
		fmt.Print(os.Getenv("FAKE_CLAUDE_PRINT"))
		return
	}
	fixture, err := os.Open(os.Getenv("FAKE_CLAUDE_FIXTURE"))
	if err != nil {
		fail(2, "fakeclaude: FAKE_CLAUDE_FIXTURE: %v", err)
	}
	defer fixture.Close()
	if path := os.Getenv("FAKE_CLAUDE_CWD_LOG"); path != "" {
		cwd, _ := os.Getwd()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fail(1, "cwd log: %v", err)
		}
		_, _ = f.WriteString(cwd + "\n")
		_ = f.Close()
	}
	if os.Getenv("FAKE_CLAUDE_VALIDATE_RESUME") != "" {
		history(os.Args[1:])
	}
	if path := os.Getenv("FAKE_CLAUDE_MCP_LOG"); path != "" {
		probeMCP(path, os.Args[1:])
	}
	life("start")
	if path := os.Getenv("FAKE_CLAUDE_ENV_LOG"); path != "" {
		config, ok := os.LookupEnv("CLAUDE_CONFIG_DIR")
		if !ok {
			config = "<unset>"
		}
		appendLine(path, config)
	}
	defer life("exit")
	if delay, err := time.ParseDuration(os.Getenv("FAKE_CLAUDE_EXIT_DELAY")); err == nil {
		defer time.Sleep(delay)
	}
	stdin := bufio.NewReader(os.Stdin)
	tools := map[string]string{} // open control_request id -> tool name
	var inputs []string          // uuids of the stdin user messages, in order
	rows := bufio.NewScanner(fixture)
	rows.Buffer(nil, 32<<20)
	for rows.Scan() {
		var row envelope
		if err := json.Unmarshal(rows.Bytes(), &row); err != nil {
			fail(2, "fakeclaude: fixture row: %v", err)
		}
		switch row.Direction {
		case "raw":
			var text string
			_ = json.Unmarshal(row.Message, &text)
			fmt.Println(text)
		case "wait":
			var path string
			_ = json.Unmarshal(row.Message, &path)
			for _, err := os.Stat(path); err != nil; _, err = os.Stat(path) {
				time.Sleep(10 * time.Millisecond)
			}
		case "exit":
			var code int
			_ = json.Unmarshal(row.Message, &code)
			os.Exit(code)
		case "stdout":
			for i, id := range inputs {
				row.Message = bytes.ReplaceAll(row.Message, []byte(fmt.Sprintf(`"$input:%d"`, i+1)), []byte(strconv.Quote(id)))
			}
			if hold := os.Getenv("FAKE_CLAUDE_HOLD"); hold != "" && typeOf(row.Message) == "result" {
				for _, err := os.Stat(hold); err != nil; _, err = os.Stat(hold) {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if id, tool, ok := controlRequest(row.Message); ok {
				tools[id] = tool
			}
			fmt.Printf("%s\n", row.Message)
		case "stdin":
			got, err := readInput(stdin, subtypeOf(row.Message))
			if err != nil {
				fail(3, "fakeclaude: stdin closed, expected %s", typeOf(row.Message))
			}
			if want, have := typeOf(row.Message), typeOf(got); want != have {
				fail(3, "fakeclaude: stdin message type %q, expected %q", have, want)
			}
			if want := subtypeOf(row.Message); want != "" && typeOf(got) == "control_request" && subtypeOf(got) != want {
				fail(3, "fakeclaude: control_request subtype %q, expected %q", subtypeOf(got), want)
			}
			if typeOf(got) == "user" {
				var m struct {
					UUID string `json:"uuid"`
				}
				_ = json.Unmarshal(got, &m)
				inputs = append(inputs, m.UUID)
			}
			if typeOf(row.Message) == "control_response" {
				checkControlResponse(row.Message, got, tools)
			}
		}
	}
	if err := rows.Err(); err != nil {
		fail(2, "fakeclaude: %v", err)
	}
	// Like the real CLI, stay alive until stdin closes.
	for {
		_, err := readInput(stdin, "")
		if err != nil {
			return
		}
	}
}

// checkInput rejects stdin the real CLI cannot use: anything but a JSON
// object, a user message without user content, or a control request without
// a request id and subtype. initialize needs array-valued MCP server lists
// (spike §5: `{}` was rejected).
func checkInput(got []byte) {
	var m struct {
		Type    string `json:"type"`
		Message *struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		RequestID string `json:"request_id"`
		Request   *struct {
			Subtype       string          `json:"subtype"`
			SDKMcpServers json.RawMessage `json:"sdkMcpServers"`
		} `json:"request"`
	}
	if err := json.Unmarshal(got, &m); err != nil || m.Type == "" {
		fail(3, "fakeclaude: malformed stdin line %q", bytes.TrimSpace(got))
	}
	switch m.Type {
	case "user":
		if m.Message == nil || m.Message.Role != "user" || len(m.Message.Content) == 0 || string(m.Message.Content) == "null" ||
			string(m.Message.Content) == `""` || string(m.Message.Content) == "[]" {
			fail(3, "fakeclaude: user message without user content")
		}
	case "control_request":
		if m.RequestID == "" || m.Request == nil || m.Request.Subtype == "" {
			fail(3, "fakeclaude: control_request without request_id and request.subtype")
		}
		switch m.Request.Subtype {
		case "initialize":
			var servers []string
			if json.Unmarshal(m.Request.SDKMcpServers, &servers) != nil {
				fail(3, "initialize: sdkMcpServers and webSearchIsolationExemptMcpServers must be arrays of strings")
			}
		case "interrupt", "set_model":
		default:
			reply, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error",
				"request_id": m.RequestID, "error": "Unsupported control request subtype: " + m.Request.Subtype}})
			fmt.Printf("%s\n", reply)
		}
	case "control_response":
	default:
		fail(3, "fakeclaude: unknown stdin message type %q", m.Type)
	}
}

func controlRequest(raw []byte) (id, tool string, ok bool) {
	var m struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype  string `json:"subtype"`
			ToolName string `json:"tool_name"`
		} `json:"request"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Type != "control_request" || m.Request.Subtype != "can_use_tool" {
		return "", "", false
	}
	return m.RequestID, m.Request.ToolName, true
}

// checkControlResponse rejects what the real CLI cannot use: a response to
// a request it never sent (or answered twice), or a malformed decision. Allow
// needs the tool's updated input (AskUserQuestion: questions and answers);
// deny needs a message.
func checkControlResponse(recorded, got []byte, tools map[string]string) {
	type response struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Response  *struct {
				Behavior     string         `json:"behavior"`
				UpdatedInput map[string]any `json:"updatedInput"`
				Message      string         `json:"message"`
			} `json:"response"`
		} `json:"response"`
	}
	var want, have response
	_ = json.Unmarshal(recorded, &want)
	if err := json.Unmarshal(got, &have); err != nil {
		fail(3, "fakeclaude: malformed control_response: %v", err)
	}
	id := have.Response.RequestID
	tool, open := tools[id]
	if id != want.Response.RequestID || !open {
		fail(3, "fakeclaude: control_response for request %q, expected %q", id, want.Response.RequestID)
	}
	delete(tools, id)
	decision := have.Response.Response
	switch {
	case have.Response.Subtype != "success" || decision == nil:
		fail(3, "fakeclaude: control_response without a success decision")
	case decision.Behavior == "allow" && decision.UpdatedInput == nil:
		fail(3, "fakeclaude: allow without updatedInput")
	case decision.Behavior == "allow" && tool == "AskUserQuestion" &&
		(decision.UpdatedInput["questions"] == nil || decision.UpdatedInput["answers"] == nil):
		fail(3, "fakeclaude: AskUserQuestion allow needs questions and answers in updatedInput")
	case decision.Behavior == "deny" && decision.Message == "":
		fail(3, "fakeclaude: deny without a message")
	case decision.Behavior != "allow" && decision.Behavior != "deny":
		fail(3, "fakeclaude: unknown behavior %q", decision.Behavior)
	}
}

// subtypeOf is a control request's request.subtype. A recorded stdin row
// that names one is matched exactly, so an interrupt fixture cannot be
// satisfied by another (or an unsupported) control request.
func subtypeOf(raw []byte) string {
	var m struct {
		Request struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Request.Subtype
}

func typeOf(raw []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Type
}

func fail(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}

func logStdin(line []byte) {
	path := os.Getenv("FAKE_CLAUDE_STDIN")
	if path == "" || len(line) == 0 {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail(1, "stdin log: %v", err)
	}
	_, _ = f.Write(line)
	_ = f.Close()
}

func contains(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func plugin(args []string) {
	if path := os.Getenv("FAKE_CLAUDE_PLUGIN_LOG"); path != "" {
		data, _ := json.Marshal(args)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fail(1, "plugin log: %v", err)
		}
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}
	if len(args) > 0 && args[0] == os.Getenv("FAKE_CLAUDE_PLUGIN_FAIL") {
		fail(1, "plugin %s failed", args[0])
	}
	if len(args) < 2 || (args[0] != "install" && args[0] != "uninstall") {
		return
	}
	path := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			fail(1, "settings: %v", err)
		}
	}
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	if enabled == nil {
		enabled = map[string]any{}
	}
	if args[0] == "install" {
		enabled[args[1]] = true
	} else {
		delete(enabled, args[1])
	}
	settings["enabledPlugins"] = enabled
	if len(enabled) == 0 {
		delete(settings, "enabledPlugins")
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fail(1, "settings: %v", err)
	}
}

func life(event string) {
	if path := os.Getenv("FAKE_CLAUDE_LIFE_LOG"); path != "" {
		appendLine(path, fmt.Sprintf("%s %d", event, time.Now().UnixNano()))
	}
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail(1, "log %s: %v", path, err)
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

// history keeps session ids per configuration, so a resume under another
// CLAUDE_CONFIG_DIR fails like the real CLI's.
func history(args []string) {
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "fake-history")
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--resume":
			if _, err := os.Stat(filepath.Join(dir, args[i+1])); err != nil {
				fail(1, "No conversation found with session ID: %s", args[i+1])
			}
		case "--session-id":
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fail(1, "history: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, args[i+1]), nil, 0o600); err != nil {
				fail(1, "history: %v", err)
			}
		}
	}
}

// probeMCP connects to the --mcp-config servers like the CLI does at start:
// an MCP initialize over streamable HTTP, answered with serverInfo.
func probeMCP(log string, args []string) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--mcp-config" {
			continue
		}
		var config struct {
			MCPServers map[string]struct {
				URL string `json:"url"`
			} `json:"mcpServers"`
		}
		_ = json.Unmarshal([]byte(args[i+1]), &config)
		for _, server := range config.MCPServers {
			appendLine(log, server.URL+" "+initializeMCP(server.URL))
		}
	}
}

func initializeMCP(url string) string {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"fakeclaude","version":"0"}}}`
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "failed: " + err.Error()
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"serverInfo"`)) {
		return fmt.Sprintf("failed: %d %s", resp.StatusCode, bytes.TrimSpace(data))
	}
	return "ok"
}
