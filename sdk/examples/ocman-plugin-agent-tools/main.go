// ocman-plugin-agent-tools serves one of the deterministic tool.v1 example
// workflows from package agenttools. Build it as ocman-plugin-notes-example
// or ocman-plugin-inventory-example; the executable name selects the workflow.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/ocman/ocman/sdk/examples/agenttools"
	"github.com/ocman/ocman/sdk/plugin"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	p := agenttools.Notes("org.example.notes")
	if strings.Contains(filepath.Base(os.Args[0]), "inventory") {
		p = agenttools.Inventory("org.example.inventory")
	}
	if err := plugin.Run(context.Background(), plugin.Mode(os.Args[1]), os.Getenv("OCMAN_PLUGIN_TOKEN"), p.Description, os.Stdin, os.Stdout, p.Handler); err != nil {
		os.Exit(1)
	}
}
