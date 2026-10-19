package opencode

import (
	"context"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// OpenCode v2 lists skills apart from commands and rejects them on its
// command route; the adapter still offers them as skills and runs them.
func TestSkillsListAndRunOnV2(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	const sid, dir = "sess-v2-skills", "/tmp/proj-v2-skills"
	t.Cleanup(func() { catalogCache.invalidatePort("") })
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/command":
			writeJSONBody(w, `{"data":[{"name":"init","description":"setup"}]}`)
		case "GET /api/skill":
			writeJSONBody(w, `{"data":[{"id":"review-pr","name":"review-pr","description":"Review a PR","path":"/s","content":"x"}]}`)
		case "POST /api/session/" + sid + "/command":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"_tag":"CommandNotFoundError","command":"review-pr","message":"not found"}`))
		case "GET /api/session/" + sid:
			writeJSONBody(w, `{"data":{"id":"`+sid+`","location":{"directory":"`+dir+`"}}}`)
		case "POST /api/session/" + sid + "/prompt":
			writeJSONBody(w, `{"data":{"id":"msg1"}}`)
		default:
			return false
		}
		return true
	})
	a := nativeQueueAdapter(t, sid, dir, f)
	catalogCache.invalidatePort(f.Port())

	entries, err := a.SlashCommands(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	bySource := map[string]string{}
	for _, e := range entries {
		bySource[e.Name] = e.Source
	}
	if bySource["init"] != "command" || bySource["review-pr"] != "skill" {
		t.Fatalf("entries = %+v, want init command and review-pr skill", entries)
	}

	if err := a.ExecuteCommand(context.Background(), platforms.ExecuteCommandRequest{SessionID: sid, Command: "review-pr", Arguments: "1118"}); err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	hit, ok := f.find(http.MethodPost, "/api/session/"+sid+"/prompt")
	if !ok || hit.body["text"] != "@review-pr 1118" {
		t.Fatalf("prompt = %+v, want the @review-pr mention", hit.body)
	}
}
