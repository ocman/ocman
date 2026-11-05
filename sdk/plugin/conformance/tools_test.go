package conformance_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/sdk/internal/testutil"
	"github.com/ocman/ocman/sdk/plugin"
	"github.com/ocman/ocman/sdk/plugin/conformance"
)

func TestAgentToolExamples(t *testing.T) {
	dir := t.TempDir()
	for name, inv := range map[string]plugin.ToolInvocation{
		"ocman-plugin-notes-example":     {Tool: "notes_search", Arguments: json.RawMessage(`{"query":"plan"}`)},
		"ocman-plugin-inventory-example": {Tool: "inventory", Arguments: json.RawMessage(`{"operation":"help"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			if out, err := exec.Command("go", "build", "-o", path, "../../examples/ocman-plugin-agent-tools").CombinedOutput(); err != nil {
				t.Fatalf("build: %s: %v", out, err)
			}
			testutil.WarmExecutable(t, path)
			conformance.RunTools(t, path, inv)
		})
	}
}
