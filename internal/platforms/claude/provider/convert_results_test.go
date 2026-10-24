package claudeprovider

import (
	"encoding/json"
	"testing"
)

func TestConverterStructuredToolResults(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		output  string
		images  int
	}{
		{"tool references", `[{"type":"tool_reference","tool_name":"Agent"},{"type":"tool_reference","tool_name":"Bash"}]`, "Available tool: Agent\nAvailable tool: Bash", 0},
		{"mixed text and image", `[{"type":"text","text":"Screenshot"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]`, "Screenshot", 1},
		{"image only", `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]`, "", 1},
		{"invalid images", `[{"type":"image","source":{"media_type":"text/html","data":"aGVsbG8="}},{"type":"image","source":{"media_type":"image/png","data":""}}]`, "", 0},
		{"empty tool reference", `[{"type":"tool_reference","tool_name":""}]`, "", 0},
		{"plain string", `"hello"`, "hello", 0},
		{"unknown shape", `{"unexpected":true}`, `{"unexpected":true}`, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Converter{SessionID: "session"}
			c.Add([]byte(`{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"tool","name":"ToolSearch","input":{"query":"Agent"}}]}}`))
			c.Add([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":` + tt.content + `}]}}`))
			c.Finish()
			if len(c.Parts) != 1 {
				t.Fatalf("parts = %d", len(c.Parts))
			}
			var part struct {
				State struct {
					Status      string `json:"status"`
					Output      string `json:"output"`
					Attachments []struct {
						Type string `json:"type"`
						Mime string `json:"mime"`
						URL  string `json:"url"`
					} `json:"attachments"`
				} `json:"state"`
			}
			if err := json.Unmarshal(c.Parts[0].Data, &part); err != nil {
				t.Fatal(err)
			}
			if part.State.Output != tt.output || part.State.Status != "completed" {
				t.Errorf("status/output = %q/%q, want completed/%q", part.State.Status, part.State.Output, tt.output)
			}
			if len(part.State.Attachments) != tt.images {
				t.Fatalf("attachments = %d, want %d", len(part.State.Attachments), tt.images)
			}
			for _, image := range part.State.Attachments {
				if image.Type != "file" || image.Mime != "image/png" || image.URL != "data:image/png;base64,aGVsbG8=" {
					t.Errorf("attachment = %+v", image)
				}
			}
		})
	}
}
