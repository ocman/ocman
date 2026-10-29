package claudeprovider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/conversationingest"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/statev2"
)

// Claude writes each subagent's launching Agent block into its transcript,
// with a fresh line UUID but the parent's API message.id. Two launches can
// belong to the same API response. This is not a user-created session fork.
func TestHistorySubagentBootstrapSharesNativeMessageIdentity(t *testing.T) {
	config := t.TempDir()
	project := filepath.Join(config, "projects", "-work-repo")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	block := func(uuid, tool, child string) string {
		return fmt.Sprintf(`{"type":"assistant","uuid":%q,"sessionId":%q,"agentId":%q,"cwd":"/work/repo","timestamp":"2026-01-01T00:00:01Z","message":{"id":"msg_shared","model":"claude-test","content":[{"type":"tool_use","id":%q,"name":"Agent","input":{"description":"Synthetic task"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":2}}}`+"\n", uuid, mainID, child, tool)
	}
	write(filepath.Join(project, mainID+".jsonl"), block("parent-line-a", "tool-a", "")+block("parent-line-b", "tool-b", ""))
	for _, child := range []struct{ id, tool string }{{"a", "tool-a"}, {"b", "tool-b"}} {
		ref := fmt.Sprintf(`{"type":"fork-context-ref","agentId":%q,"parentSessionId":%q,"parentLastUuid":"context-end","contextLength":1}`+"\n", child.id, mainID)
		write(filepath.Join(project, mainID, "subagents", "agent-"+child.id+".jsonl"), ref+block("child-line-"+child.id, child.tool, child.id))
	}
	history := &HistoryReader{index: openSynced(t, config)}
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := statev2.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "claude", OwnerID: "owner", Platform: "claudecode", SourceKey: config}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{mainID, "agent-a", "agent-b"} {
		snapshot, err := history.Snapshot(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Messages) != 1 || snapshot.Messages[0].ID != "msg_shared" {
			t.Fatalf("%s: native identity changed: %+v", id, snapshot.Messages)
		}
		if id != mainID && (!snapshot.Subagent || snapshot.Session.ParentID != mainID) {
			t.Fatalf("%s: lost native child ownership", id)
		}
		key := statev2.Key{InstanceID: "claude", SessionID: id}
		batch, _, err := conversationingest.Normalize(key, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
		page, err := store.ReadConversation(t.Context(), key, 10, 0)
		if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "msg_shared" {
			t.Fatalf("%s: retained identity: %+v %v", id, page, err)
		}
		wantTools := []string{"tool-a", "tool-b"}
		if id == "agent-a" {
			wantTools = []string{"tool-a"}
		} else if id == "agent-b" {
			wantTools = []string{"tool-b"}
		}
		if len(page.Parts) != len(wantTools) {
			t.Fatalf("%s: parts crossed session boundaries: %+v", id, page.Parts)
		}
		for i, part := range page.Parts {
			var metadata struct {
				CallID string `json:"callID"`
			}
			if err := json.Unmarshal(part.Extensions, &metadata); err != nil {
				t.Fatal(err)
			}
			if part.MessageID != "msg_shared" || metadata.CallID != wantTools[i] {
				t.Fatalf("%s: shared part ID merged unrelated tools: %+v", id, part)
			}
		}
	}
	reader, err := db.OpenHistoryAnalytics(path, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	scoped, release, err := reader.ScopedHistoryReader(t.Context())
	if err != nil {
		t.Fatalf("native subagent bootstrap blocked analytics: %v", err)
	}
	defer release()
	requests, err := scoped.GetMetricsLog(t.Context(), db.MetricsDashboardOptions{}, db.MetricsLogRequests)
	if err != nil || len(requests.Requests) != 3 {
		t.Fatalf("session-qualified requests lost: %+v %v", requests, err)
	}
	seen := map[string]bool{}
	for _, request := range requests.Requests {
		if request.ID != "msg_shared" || seen[request.SessionID] {
			t.Fatalf("request identities merged or rewritten: %+v", requests.Requests)
		}
		seen[request.SessionID] = true
	}
}
