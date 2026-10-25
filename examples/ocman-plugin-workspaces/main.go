// ocman-plugin-workspaces demonstrates host-owned forms, lists and detail pages.
// It stores configuration only in its owner-assigned private working directory.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func description() plugin.Description {
	return plugin.Description{
		ID: "org.ocman.workspaces", Name: "Workspace example", Version: "1.0.0",
		Protocol: plugin.Version{Major: 1}, Scope: plugin.ScopeOwner, MaxConcurrency: 2,
		Capabilities:    []plugin.Capability{plugin.PageContributionCapability},
		RequestedGrants: []string{plugin.PageProjectGrant},
		Pages:           []plugin.PageDescriptor{{ID: "services", Label: "Example services", Validate: true}},
	}
}

func fields() []plugin.PageField {
	minimum, maximum, maxRows := 1.0, 20.0, 20
	return []plugin.PageField{{Key: "services", Label: "Services", Type: "list", MaxItems: &maxRows, Fields: []plugin.PageField{
		{Key: "name", Label: "Name", Type: "text", Required: true, Help: "Use a unique name. The name reserved demonstrates a validation error."},
		{Key: "replicas", Label: "Replicas", Type: "integer", Required: true, Minimum: &minimum, Maximum: &maximum},
		{Key: "environment", Label: "Environment", Type: "select", Required: true, Options: []plugin.PageOption{{Value: "staging", Label: "Staging"}, {Value: "production", Label: "Production"}}},
	}}}
}

type workspace struct{ mu sync.Mutex }

func load(project string) (plugin.PageDocument, string, error) {
	path := fmt.Sprintf("services-%x.json", sha256.Sum256([]byte(project)))
	raw, err := os.ReadFile(path)
	configured := err == nil
	if os.IsNotExist(err) {
		raw, err = []byte(`{"services":[]}`), nil
	}
	if err != nil {
		return plugin.PageDocument{}, path, err
	}
	doc := plugin.PageDocument{Revision: fmt.Sprintf("%x", sha256.Sum256(raw)), Fields: fields(), Values: raw, Configured: &configured}
	return doc, path, nil
}

func issues(values json.RawMessage) []plugin.PageIssue {
	var object map[string]any
	if json.Unmarshal(values, &object) != nil || object == nil {
		return []plugin.PageIssue{{Message: "Expected a form object."}}
	}
	result := plugin.ValidatePageInput(fields(), object)
	if len(result) > 0 {
		return result
	}
	seen := map[string]bool{}
	rows, _ := object["services"].([]any)
	for i, value := range rows {
		row := value.(map[string]any)
		name := row["name"].(string)
		if name == "reserved" || seen[name] {
			result = append(result, plugin.PageIssue{Path: fmt.Sprintf("/services/%d/name", i), Message: "Choose a unique, non-reserved name."})
		}
		seen[name] = true
	}
	return result
}

func (w *workspace) handle(ctx context.Context, method string, req plugin.PageRequest) (plugin.PageDocument, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return plugin.PageDocument{}, err
	}
	doc, path, err := load(req.ProjectID)
	if err != nil {
		return doc, err
	}
	if req.Cursor != "" {
		return doc, plugin.Failure(plugin.ErrorInvalidArgument)
	}
	if method != "read" {
		if req.ItemID != "" {
			return doc, plugin.Failure(plugin.ErrorInvalidArgument)
		}
		if req.Revision != doc.Revision {
			return doc, plugin.Failure(plugin.ErrorConflict)
		}
		doc.Issues = issues(req.Values)
		if len(doc.Issues) > 0 || method == "validate" {
			return doc, nil
		}
		if err = writePrivate(path, req.Values); err != nil {
			return doc, err
		}
		doc, _, err = load(req.ProjectID)
		if err != nil {
			return doc, err
		}
	}
	return present(doc, req.ItemID)
}

func present(doc plugin.PageDocument, itemID string) (plugin.PageDocument, error) {
	var values struct {
		Services []struct {
			Name        string `json:"name"`
			Replicas    int    `json:"replicas"`
			Environment string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(doc.Values, &values); err != nil {
		return doc, err
	}
	collection := &plugin.PageCollection{Title: "Services", Columns: []plugin.PageColumn{
		{Key: "name", Label: "Name", Display: "text"}, {Key: "replicas", Label: "Replicas", Display: "number"}, {Key: "environment", Label: "Environment", Display: "text"},
	}, Rows: []plugin.PageRow{}}
	for _, service := range values.Services {
		id := fmt.Sprintf("service-%x", sha256.Sum256([]byte(service.Name)))
		if itemID == id {
			doc.Fields, doc.Values = nil, json.RawMessage(`{}`)
			doc.Detail = &plugin.PageDetail{Title: service.Name, Properties: []plugin.PageProperty{
				{Label: "Replicas", Display: "number", Value: service.Replicas}, {Label: "Environment", Display: "text", Value: service.Environment},
			}}
			return doc, nil
		}
		collection.Rows = append(collection.Rows, plugin.PageRow{ID: id, Values: map[string]any{"name": service.Name, "replicas": service.Replicas, "environment": service.Environment}})
	}
	if itemID != "" {
		return doc, plugin.Failure(plugin.ErrorNotFound)
	}
	doc.Collection = collection
	return doc, nil
}

func writePrivate(path string, values []byte) error {
	f, err := os.CreateTemp(".", ".services-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(values); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	mode := plugin.Mode(os.Args[1])
	if mode == plugin.ModeServe {
		if _, err := plugin.ReadConfiguration(); err != nil {
			os.Exit(1)
		}
	}
	w := &workspace{}
	if err := plugin.Run(context.Background(), mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), description(), os.Stdin, os.Stdout, plugin.PageHandler(description(), w.handle)); err != nil {
		os.Exit(1)
	}
}
