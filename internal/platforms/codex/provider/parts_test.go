package codexprovider

import (
	"encoding/json"
	"strings"
	"testing"

	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

func state(p map[string]any) map[string]any { return p["state"].(map[string]any) }

func TestItemPartTools(t *testing.T) {
	exit := 2
	out := "boom"
	cases := []struct {
		name  string
		item  codexappserver.Item
		check func(map[string]any) bool
	}{
		{"failed command keeps its output as the error", codexappserver.Item{Type: "commandExecution", ID: "c", Command: "false", Status: "failed", ExitCode: &exit, AggregatedOutput: &out},
			func(p map[string]any) bool {
				s := state(p)
				return s["status"] == "error" && s["error"] == "boom" && s["metadata"].(map[string]any)["exit"] == 2
			}},
		{"declined command", codexappserver.Item{Type: "commandExecution", ID: "c", Command: "rm -rf /", Status: "declined"},
			func(p map[string]any) bool { return state(p)["error"] == "Declined" }},
		{"mcp call", codexappserver.Item{Type: "mcpToolCall", ID: "m", Server: "ocman", Tool: "inbox", Status: "completed",
			Arguments: json.RawMessage(`{"action":"help"}`), Result: json.RawMessage(`{"content":[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]}`)},
			func(p map[string]any) bool {
				s := state(p)
				return p["tool"] == "ocman_inbox" && s["output"] == "a\nb" && s["input"].(map[string]any)["action"] == "help"
			}},
		{"failed mcp call", codexappserver.Item{Type: "mcpToolCall", ID: "m", Server: "s", Tool: "t", Status: "failed", ToolError: &codexappserver.ToolError{Message: "nope"}},
			func(p map[string]any) bool { return state(p)["error"] == "nope" }},
		{"dynamic tool with non-object arguments", codexappserver.Item{Type: "dynamicToolCall", ID: "d", Tool: "lookup", Status: "completed", Arguments: json.RawMessage(`"q"`)},
			func(p map[string]any) bool {
				return p["tool"] == "lookup" && state(p)["input"].(map[string]any)["arguments"] == `"q"`
			}},
		{"dynamic tool without arguments", codexappserver.Item{Type: "dynamicToolCall", ID: "d", Tool: "t", Arguments: json.RawMessage(`null`)},
			func(p map[string]any) bool { return len(state(p)["input"].(map[string]any)) == 0 }},
		{"web search", codexappserver.Item{Type: "webSearch", ID: "w", Query: "go generics"},
			func(p map[string]any) bool {
				return p["tool"] == "websearch" && state(p)["status"] == "completed" && state(p)["title"] == "go generics"
			}},
		{"image view", codexappserver.Item{Type: "imageView", ID: "i", Path: "/r/a.png", CompletedAtMs: 7},
			func(p map[string]any) bool {
				return p["tool"] == "read" && state(p)["input"].(map[string]any)["filePath"] == "/r/a.png" &&
					state(p)["time"].(map[string]any)["end"] == int64(7)
			}},
		{"plan", codexappserver.Item{Type: "plan", ID: "p", Text: "1. a"},
			func(p map[string]any) bool { return p["type"] == "text" && p["text"] == "1. a" }},
		{"reasoning falls back to raw content", codexappserver.Item{Type: "reasoning", ID: "r", Summary: []string{" "}, ReasoningContent: []string{"a", "b"}},
			func(p map[string]any) bool { return p["type"] == "reasoning" && p["text"] == "a\n\nb" }},
	}
	for _, tc := range cases {
		p := ItemPart(tc.item, 1)
		if p == nil || !tc.check(p) {
			t.Errorf("%s: part = %v", tc.name, p)
		}
	}
	if p := ItemPart(codexappserver.Item{Type: "contextCompaction", ID: "x"}, 1); p["type"] != "text" || p["text"] != "Conversation compacted." {
		t.Errorf("compaction = %v", p)
	}
	for _, typ := range []string{"enteredReviewMode", "exitedReviewMode", "somethingNew"} {
		if p := ItemPart(codexappserver.Item{Type: typ, ID: "x"}, 1); p != nil {
			t.Errorf("%s converted to %v", typ, p)
		}
	}
	if out := mcpOutput(json.RawMessage(`[`)); out != "" {
		t.Errorf("mcpOutput(invalid) = %q", out)
	}
}

func TestPatch(t *testing.T) {
	changes := []codexappserver.FileUpdate{
		{Path: "new.txt", Kind: codexappserver.FileUpdateKind{Type: "add"}, Diff: "one\ntwo\n"},
		{Path: "old.txt", Kind: codexappserver.FileUpdateKind{Type: "delete"}, Diff: "gone\n"},
		{Path: "a.go", Kind: codexappserver.FileUpdateKind{Type: "update", MovePath: "b.go"}, Diff: "diff --git a/a.go b/b.go\nindex 1..2\n--- a/a.go\n+++ b/b.go\n@@ -1 +1 @@\n-x\n+y"},
		{Path: "empty.txt", Kind: codexappserver.FileUpdateKind{Type: "add"}},
	}
	want := "*** Begin Patch\n" +
		"*** Add File: new.txt\n+one\n+two\n" +
		"*** Delete File: old.txt\n" +
		"*** Update File: a.go\n*** Move to: b.go\n@@ -1 +1 @@\n-x\n+y\n" +
		"*** Add File: empty.txt\n" +
		"*** End Patch"
	if got := Patch(changes); got != want {
		t.Fatalf("Patch =\n%s\nwant\n%s", got, want)
	}
	// Inside a hunk, a deleted "-- heading" and an added "++ heading" look
	// like file headers but are content.
	hunk := []codexappserver.FileUpdate{{Path: "a.md", Kind: codexappserver.FileUpdateKind{Type: "update"},
		Diff: "--- a/a.md\n+++ b/a.md\n@@ -1,2 +1,2 @@\n--- heading\n+++ heading\n index stays\n"}}
	if got, want := Patch(hunk), "*** Begin Patch\n*** Update File: a.md\n@@ -1,2 +1,2 @@\n--- heading\n+++ heading\n index stays\n*** End Patch"; got != want {
		t.Fatalf("Patch =\n%s\nwant\n%s", got, want)
	}
	p := ItemPart(codexappserver.Item{Type: "fileChange", ID: "f", Status: "inProgress", Changes: changes[:2]}, 1)
	if state(p)["title"] != "new.txt, old.txt" || state(p)["status"] != "running" {
		t.Fatalf("fileChange part = %v", p)
	}
}

func TestUnwrapShell(t *testing.T) {
	cases := map[string]string{
		"/bin/zsh -lc ls":                           "ls",
		"/bin/zsh -lc 'sleep 30'":                   "sleep 30",
		`/bin/zsh -lc "printf 'hi\\n' > hello.txt"`: `printf 'hi\n' > hello.txt`,
		`bash -lc 'it'\''s'`:                        "it's",
		`sh -c "a \"b\" \$HOME"`:                    `a "b" $HOME`,
		"/bin/zsh -lc 'line one\nline two'":         "line one\nline two",
		"/bin/zsh -lc a b":                          "/bin/zsh -lc a b",
		"git status":                                "git status",
	}
	for in, want := range cases {
		if got := unwrapShell(in); got != want {
			t.Errorf("unwrapShell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUserShellCommand(t *testing.T) {
	out := "shell-ok\n"
	p := ItemPart(codexappserver.Item{Type: "commandExecution", ID: "s", Command: "/bin/zsh -lc 'echo shell-ok'", Source: "userShell", Status: "completed", AggregatedOutput: &out}, 1)
	if state(p)["metadata"].(map[string]any)["ocmanUserExecutedShell"] != true || state(p)["input"].(map[string]any)["command"] != "echo shell-ok" {
		t.Fatalf("user shell part = %v", p)
	}
	agent := ItemPart(codexappserver.Item{Type: "commandExecution", ID: "a", Command: "ls", Source: "agent", Status: "completed"}, 1)
	if _, ok := state(agent)["metadata"].(map[string]any)["ocmanUserExecutedShell"]; ok {
		t.Fatal("agent command marked as the user's")
	}
}

func TestFileEntries(t *testing.T) {
	entries := fileEntries([]codexappserver.FileUpdate{
		{Path: "/r/new.txt", Kind: codexappserver.FileUpdateKind{Type: "add"}, Diff: "one\ntwo\n"},
		{Path: "/r/old.txt", Kind: codexappserver.FileUpdateKind{Type: "delete"}, Diff: "gone\n"},
		{Path: "/r/a.go", Kind: codexappserver.FileUpdateKind{Type: "update", MovePath: "/r/b.go"},
			Diff: "--- a/a.go\n+++ b/b.go\n@@ -1,2 +1,2 @@\n-x\n--- heading\n+y\n+++ heading\n ctx\n"},
		{Path: "/r/empty.txt", Kind: codexappserver.FileUpdateKind{Type: "add"}},
	})
	type want struct {
		path, kind      string
		additions, dels int
		patchHas        string
	}
	for i, w := range []want{
		{"/r/new.txt", "add", 2, 0, "--- /dev/null\n+++ /r/new.txt\n@@ -0,0 +1,2 @@\n+one\n+two\n"},
		{"/r/old.txt", "delete", 0, 1, "--- /r/old.txt\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-gone\n"},
		{"/r/b.go", "move", 2, 2, "--- /r/a.go\n+++ /r/b.go\n@@ -1,2 +1,2 @@\n-x\n--- heading\n+y\n+++ heading\n ctx\n"},
		{"/r/empty.txt", "add", 0, 0, "+++ /r/empty.txt\n"},
	} {
		e := entries[i]
		if e["filePath"] != w.path || e["type"] != w.kind || e["additions"] != w.additions || e["deletions"] != w.dels ||
			!strings.Contains(e["patch"].(string), w.patchHas) || !strings.HasPrefix(e["patch"].(string), "Index: "+w.path+"\n") {
			t.Errorf("entry %d = %v", i, e)
		}
	}
}
