package ocv2

import "testing"

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
