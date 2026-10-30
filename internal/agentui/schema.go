// Package agentui validates self-contained A2UI results for conversation rendering.
package agentui

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const CatalogID = "https://ocman.dev/a2ui/results/v1/catalog.json"
const schemaBase = "https://ocman.dev/a2ui/results/v1/"
const MaxBytes = 64 * 1024

//go:embed *.json
var schemas embed.FS

var compiled = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	for _, name := range []string{"common_types.json", "catalog.json", "result.schema.json"} {
		var doc any
		if err := json.Unmarshal(schemaBytes(name), &doc); err != nil {
			return nil, err
		}
		if err := c.AddResource(schemaBase+name, doc); err != nil {
			return nil, err
		}
	}
	return c.Compile(schemaBase + "result.schema.json")
})

func schemaBytes(name string) []byte {
	b, _ := schemas.ReadFile(name) // names are fixed embedded files
	return b
}

type Result struct {
	Title    string           `json:"title"`
	Summary  string           `json:"summary"`
	Messages []map[string]any `json:"messages"`
}

// Validate checks the shared schema and bounds the final component graph.
// Calls are independent snapshots: no server-side surface state or session mutation.
func Validate(raw []byte) (*Result, error) {
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("result exceeds %d bytes", MaxBytes)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	schema, err := compiled()
	if err != nil {
		return nil, fmt.Errorf("compile A2UI schema: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return nil, err
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if err := validateGraph(result.Messages); err != nil {
		return nil, err
	}
	return &result, nil
}

func validateGraph(messages []map[string]any) error {
	surface := ""
	components := map[string]map[string]any{}
	for _, message := range messages {
		for kind, value := range message {
			if kind == "version" {
				continue
			}
			body := value.(map[string]any)
			id := body["surfaceId"].(string)
			if kind == "createSurface" {
				if surface != "" {
					return fmt.Errorf("exactly one createSurface is allowed per result")
				}
				surface = id
				continue
			}
			if surface == "" || id != surface {
				return fmt.Errorf("createSurface must precede updates to the same surfaceId")
			}
			if kind == "updateComponents" {
				for _, item := range body["components"].([]any) {
					component := item.(map[string]any)
					components[component["id"].(string)] = component
				}
			}
		}
	}
	if len(components) > 100 {
		return fmt.Errorf("at most 100 components are allowed")
	}
	if components["root"] == nil {
		return fmt.Errorf("a root component is required")
	}
	visits := 0
	var walk func(string, map[string]bool, int) error
	walk = func(id string, ancestors map[string]bool, depth int) error {
		visits++
		if depth > 16 || visits > 500 || ancestors[id] {
			return fmt.Errorf("component graph contains a cycle or exceeds depth/render limits")
		}
		component := components[id]
		if component == nil {
			return fmt.Errorf("missing child component %q", id)
		}
		ancestors[id] = true
		defer delete(ancestors, id)
		if child, ok := component["child"].(string); ok {
			if err := walk(child, ancestors, depth+1); err != nil {
				return err
			}
			if component["component"] == "Button" && components[child]["component"] != "Text" {
				return fmt.Errorf("button child must be Text")
			}
		}
		if children, ok := component["children"].([]any); ok {
			for _, child := range children {
				if err := walk(child.(string), ancestors, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Validate disconnected definitions too, not just the visible root.
	for id := range components {
		visits = 0
		if err := walk(id, map[string]bool{}, 0); err != nil {
			return err
		}
	}
	return nil
}
