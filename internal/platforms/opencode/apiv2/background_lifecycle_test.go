package ocv2

import (
	"io"
	"testing"
)

func TestBackgroundLifecycleUsesOnlyExplicitMetadata(t *testing.T) {
	a := &activity{shells: map[string]string{}}
	for _, tc := range []struct{ id, command, lifecycle string }{
		{"server", "pnpm dev", "service"}, {"watch", "pnpm dev", ""}, {"tests", "pnpm test", "task"},
	} {
		a.addShell(map[string]any{"id": tc.id, "command": tc.command, "status": "running", "metadata": map[string]any{"sessionID": "session", "lifecycle": tc.lifecycle}})
	}
	tasks := a.backgroundDetails()["session"]
	if len(tasks) != 3 {
		t.Fatal(tasks)
	}
	for _, task := range tasks {
		if (task["lifecycle"] == "service") != (task["id"] == "server") {
			t.Fatalf("guessed lifecycle: %+v", task)
		}
	}
}

func TestBackgroundLifecycleCleanup(t *testing.T) {
	for _, event := range []string{"shell.exited", "shell.deleted", "session.deleted"} {
		t.Run(event, func(t *testing.T) {
			a := &activity{shells: map[string]string{}}
			for _, shell := range []struct{ id, owner string }{{"target", "session"}, {"other", "another-session"}} {
				a.addShell(map[string]any{"id": shell.id, "status": "running", "metadata": map[string]any{"sessionID": shell.owner, "lifecycle": "service"}})
			}
			translator := &translator{ctx: t.Context(), activity: a, out: io.Discard}
			if err := translator.handle(v2Event{Type: event, Data: map[string]any{"id": "target", "sessionID": "session"}}); err != nil {
				t.Fatal(err)
			}
			if _, exists := a.lifecycles["target"]; exists {
				t.Fatal("completed shell retained lifecycle metadata")
			}
			if a.lifecycles["other"] != "service" {
				t.Fatal("cleanup removed another session's lifecycle metadata")
			}
		})
	}
}
