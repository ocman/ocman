package opencode

import (
	"encoding/json"
	"testing"
)

// TestConvertOpenCodeMessages_AssistantRole pins the assistant
// message conversion path, which is the most data-heavy of the three
// roles (carries tokens / cost / model id in `info`).
func TestConvertOpenCodeMessages_AssistantRole(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":         "m-asst",
				"sessionID":  "s",
				"role":       "assistant",
				"providerID": "anthropic",
				"modelID":    "claude-opus-4-5",
				"tokens": map[string]interface{}{
					"input":  float64(100),
					"output": float64(50),
				},
				"cost": float64(0.0042),
			},
			"parts": []interface{}{
				map[string]interface{}{
					"id":        "p1",
					"messageID": "m-asst",
					"sessionID": "s",
					"type":      "text",
					"text":      "hello world",
				},
			},
		},
	}
	messages, parts := convertOpenCodeMessages(input)
	if len(messages) != 1 || len(parts) != 1 {
		t.Fatalf("messages=%d, parts=%d; want 1,1", len(messages), len(parts))
	}
	data := messages[0]["data"].(map[string]interface{})
	if data["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", data["role"])
	}
	if data["modelID"] != "claude-opus-4-5" {
		t.Errorf("modelID lost in conversion: %v", data["modelID"])
	}
}

// TestConvertOpenCodeMessages_SystemRole confirms system messages
// pass through as well.
func TestConvertOpenCodeMessages_SystemRole(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":   "m-sys",
				"role": "system",
			},
		},
	}
	messages, _ := convertOpenCodeMessages(input)
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	data := messages[0]["data"].(map[string]interface{})
	if data["role"] != "system" {
		t.Errorf("role = %v, want system", data["role"])
	}
}

// TestConvertOpenCodeMessages_ToolPart exercises the tool part branch
// (a tool call with state). The conversion must preserve every key
// in `data` so the frontend can render arguments + output.
func TestConvertOpenCodeMessages_ToolPart(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":   "m1",
				"role": "assistant",
			},
			"parts": []interface{}{
				map[string]interface{}{
					"id":        "p-tool",
					"messageID": "m1",
					"sessionID": "s",
					"type":      "tool",
					"tool":      "bash",
					"state": map[string]interface{}{
						"status": "completed",
						"output": "command output",
					},
				},
			},
		},
	}
	_, parts := convertOpenCodeMessages(input)
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	data := parts[0]["data"].(map[string]interface{})
	if data["tool"] != "bash" {
		t.Errorf("tool = %v, want bash", data["tool"])
	}
	if data["type"] != "tool" {
		t.Errorf("type = %v, want tool", data["type"])
	}
}

func TestConvertOpenCodeMessages_MarksSynthesizedShellTool(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":   "m-shell",
				"role": "assistant",
			},
			"parts": []interface{}{
				map[string]interface{}{
					"id":        "p-tool",
					"messageID": "m-shell",
					"sessionID": "s",
					"type":      "tool",
					"tool":      "bash",
					"state": map[string]interface{}{
						"status": "completed",
						"input":  map[string]interface{}{"command": "ls"},
						"output": "file.txt",
					},
				},
			},
		},
	}
	_, parts := convertOpenCodeMessages(input)
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	data := parts[0]["data"].(map[string]interface{})
	state := data["state"].(map[string]interface{})
	metadata := state["metadata"].(map[string]interface{})
	if metadata["ocmanUserExecutedShell"] != true {
		t.Fatalf("ocmanUserExecutedShell = %v, want true", metadata["ocmanUserExecutedShell"])
	}
}

// TestConvertOpenCodeMessages_FilePart exercises the file-attachment
// part type. ocman's frontend doesn't render the binary contents but
// it does render the filename + mime type, so both must survive the
// round-trip.
func TestConvertOpenCodeMessages_FilePart(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":   "m1",
				"role": "user",
			},
			"parts": []interface{}{
				map[string]interface{}{
					"id":        "p-file",
					"messageID": "m1",
					"sessionID": "s",
					"type":      "file",
					"filename":  "screenshot.png",
					"mime":      "image/png",
				},
			},
		},
	}
	_, parts := convertOpenCodeMessages(input)
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	data := parts[0]["data"].(map[string]interface{})
	if data["filename"] != "screenshot.png" {
		t.Errorf("filename = %v, want screenshot.png", data["filename"])
	}
}

// TestConvertOpenCodeMessages_NonStringID exercises the wrong-type
// branch — info.id is a number rather than a string. The function
// must not panic; the message id ends up as the empty string (since
// the type assertion fails).
func TestConvertOpenCodeMessages_NonStringID_DoesNotPanic(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":   float64(5), // wrong type — JSON number, not string
				"role": "user",
			},
		},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("convertOpenCodeMessages panicked on non-string id: %v", r)
		}
	}()
	messages, _ := convertOpenCodeMessages(input)
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1 (with empty id)", len(messages))
	}
	if messages[0]["id"] != "" {
		t.Errorf("non-string id should fall back to empty string; got %v", messages[0]["id"])
	}
}

// TestConvertOpenCodeMessages_PartsNotArray exercises the wrong-type
// branch where `parts` is not an array. The function must skip parts
// without panicking.
func TestConvertOpenCodeMessages_PartsNotArray_DoesNotPanic(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info":  map[string]interface{}{"id": "m1", "role": "user"},
			"parts": "not-an-array",
		},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("convertOpenCodeMessages panicked on non-array parts: %v", r)
		}
	}()
	messages, parts := convertOpenCodeMessages(input)
	if len(messages) != 1 || len(parts) != 0 {
		t.Errorf("messages=%d, parts=%d; want 1,0", len(messages), len(parts))
	}
}

// TestConvertOpenCodeMessages_NonMapPartIsSkipped exercises a parts
// array that contains an element which is not a map (e.g. a bare
// string). It must be skipped without panicking.
func TestConvertOpenCodeMessages_NonMapPartIsSkipped(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{"id": "m1", "role": "user"},
			"parts": []interface{}{
				"not-a-map",
				map[string]interface{}{"id": "p1", "type": "text"},
			},
		},
	}
	_, parts := convertOpenCodeMessages(input)
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1 (string element skipped)", len(parts))
	}
}

// FuzzConvertOpenCodeMessages exercises the conversion path with
// arbitrary JSON inputs. The contract under test is "must not panic"
// — actual semantic correctness is covered by the table-driven tests
// above. Run with `make test-fuzz` (FR-13) or
// `go test -fuzz=FuzzConvertOpenCodeMessages -fuzztime=10s`.
func FuzzConvertOpenCodeMessages(f *testing.F) {
	// Seed corpus drawn from the canned fixtures used in the
	// table-driven tests.
	seeds := []string{
		`[]`,
		`[{}]`,
		`[{"info":{"id":"m1","role":"user"}}]`,
		`[{"info":{"id":"m1","role":"assistant","tokens":{"input":1,"output":2}}}]`,
		`[{"info":{"id":"m1","role":"user"},"parts":[{"type":"text","text":"hi"}]}]`,
		`[{"info":{"id":"m1","role":"user"},"parts":"oops"}]`,
		`[{"info":{"id":5,"role":7}}]`,
		`[{"info":null}]`,
		`[{"info":{"id":"m1"},"parts":[null,1,"x",{"type":"tool","tool":"bash","state":{"status":"completed"}}]}]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		var arr []map[string]interface{}
		if err := json.Unmarshal(raw, &arr); err != nil {
			return
		}
		// Must not panic on any well-formed JSON-shaped input.
		_, _ = convertOpenCodeMessages(arr)
	})
}
