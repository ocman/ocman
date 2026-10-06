package autoapprove

import (
	"encoding/json"
	"testing"
)

func terminalCommandPayload(command, status string) string {
	data, _ := json.Marshal(map[string]any{"part": map[string]any{
		"id": "p1", "sessionID": "s1", "messageID": "m1", "type": "tool", "tool": "bash",
		"state": map[string]any{"status": status, "input": map[string]string{"command": command}},
	}})
	return string(data)
}

func TestTerminalGitCommandHints(t *testing.T) {
	for _, tc := range []struct{ command, status, want string }{
		{"git push origin HEAD", "completed", "push"},
		{"git commit -m change", "completed", "commit"},
		{"git -C /repo push", "error", "push"},
		{"git -c core.hooksPath=/tmp commit -m change", "completed", "commit"},
		{"git status && git push", "completed", "push"},
		{"git push", "running", ""},
		{"git status", "completed", ""},
		{"git pushy", "completed", ""},
		{"echo git push", "completed", ""},
	} {
		t.Run(tc.command+tc.status, func(t *testing.T) {
			var got []string
			tee := &Tee{OnGitCommand: func(sessionID, action string) {
				if sessionID != "s1" {
					t.Errorf("session = %q", sessionID)
				}
				got = append(got, action)
			}}
			payload := terminalCommandPayload(tc.command, tc.status)
			tee.dispatchTerminalPart(payload)
			tee.dispatchTerminalPart(payload)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected hints: %v", got)
				}
			} else if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("hints = %v, want [%s] once", got, tc.want)
			}
		})
	}
}

func TestGitHintsWithoutJudgeStoreOrBrowser(t *testing.T) {
	event := "data: " + `{"type":"message.part.updated","properties":` + terminalCommandPayload("git push", "error") + "}\n\n"
	upstream := newFakeOpenCodeEventServer([]string{event})
	defer upstream.close()
	var hints []string
	svc := NewService(Deps{BroadcastGitCommand: func(sessionID, action string) {
		hints = append(hints, sessionID+":"+action)
	}})
	if err := newAutoApproveWatcher(svc).streamOnce(t.Context(), upstream.port()); err != nil {
		t.Fatal(err)
	}
	if len(hints) != 1 || hints[0] != "s1:push" {
		t.Fatalf("hints = %v", hints)
	}
}

func TestGitHintDedupIsBounded(t *testing.T) {
	calls := 0
	tee := &Tee{OnGitCommand: func(string, string) { calls++ }, gitCommandHints: make(map[string]bool)}
	for i := range 256 {
		tee.gitCommandHints[string(rune(i))] = true
	}
	tee.dispatchTerminalPart(terminalCommandPayload("git push", "completed"))
	if calls != 1 || len(tee.gitCommandHints) != 1 {
		t.Fatalf("calls=%d entries=%d", calls, len(tee.gitCommandHints))
	}
}
