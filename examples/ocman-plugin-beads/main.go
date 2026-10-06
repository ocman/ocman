// ocman-plugin-beads supplies a read-only ticket tree on the project owner.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

type beadsTicket struct {
	ID, Title, Status, IssueType, ParentID string
	Priority                               int
}

type beadsStatus struct {
	Available bool
	Tickets   []beadsTicket
	Error     string
}

func description() plugin.Description {
	return plugin.Description{
		ID: "org.ocman.beads", Name: "Beads", Version: "1.0.0",
		Protocol: plugin.Version{Major: 1}, Scope: plugin.ScopeOwner, MaxConcurrency: 4,
		Capabilities:    []plugin.Capability{plugin.PaneCapability},
		RequestedGrants: []string{plugin.PaneProjectGrant},
		Settings:        []plugin.Setting{{Key: "executable", Label: "Absolute path to bd", Type: "string", Required: true}},
		Panes:           []plugin.PaneDescriptor{{ID: "tickets", Label: "Beads"}},
	}
}

func readTree(ctx context.Context, reader *beadsReader, request plugin.PaneRead) (plugin.PaneTree, error) {
	status, err := reader.readBeadsStatus(ctx, request.Directory)
	if ctx.Err() != nil {
		return plugin.PaneTree{}, ctx.Err()
	}
	if err != nil {
		return plugin.PaneTree{}, err
	}
	tree := plugin.PaneTree{Available: status.Available, Warning: status.Error != ""}
	for _, ticket := range status.Tickets {
		tree.Nodes = append(tree.Nodes, plugin.TreeNode{
			ID: ticket.ID, Title: ticket.Title, ParentID: ticket.ParentID, Status: ticket.Status,
			Badge: "P" + strconv.Itoa(ticket.Priority), Kind: ticket.IssueType,
		})
	}
	return tree, nil
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	reader := &beadsReader{beadsRunner: execBeadsRunner{}}
	if os.Args[1] == string(plugin.ModeServe) {
		config := os.NewFile(3, "configuration")
		var values struct {
			Executable string `json:"executable"`
		}
		err := json.NewDecoder(config).Decode(&values)
		_ = config.Close()
		if err != nil || !filepath.IsAbs(values.Executable) {
			os.Exit(1)
		}
		reader.executable = values.Executable
	}
	handler := plugin.PaneHandler(description(), func(ctx context.Context, request plugin.PaneRead) (plugin.PaneTree, error) {
		return readTree(ctx, reader, request)
	})
	if plugin.Run(context.Background(), plugin.Mode(os.Args[1]), os.Getenv("OCMAN_PLUGIN_TOKEN"), description(), os.Stdin, os.Stdout, handler) != nil {
		os.Exit(1)
	}
}
