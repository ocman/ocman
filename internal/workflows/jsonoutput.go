package workflows

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func jsonOutputInstruction(schema any) string {
	raw, _ := json.Marshal(schema)
	return "\n\nIMPORTANT: Reply with exactly one JSON value matching the schema below as your entire final message. Do not include prose, explanations, or Markdown code fences.\nExpected output JSON Schema:\n" + string(raw)
}

func compileOutputSchema(schema any) (*jsonschema.Schema, error) {
	if hasExternalSchemaRef(schema) {
		return nil, fmt.Errorf("external references are not supported")
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	const location = "urn:ocman:workflow-output-schema"
	if err := compiler.AddResource(location, document); err != nil {
		return nil, err
	}
	return compiler.Compile(location)
}

func validateJSONOutput(output string, schemaDefinition any) error {
	schema, err := compileOutputSchema(schemaDefinition)
	if err != nil {
		return err
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(output))
	if err != nil {
		return err
	}
	return schema.Validate(value)
}

func hasExternalSchemaRef(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if (key == "$ref" || key == "$dynamicRef") && !strings.HasPrefix(fmt.Sprint(child), "#") {
				return true
			}
			if hasExternalSchemaRef(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasExternalSchemaRef(child) {
				return true
			}
		}
	}
	return false
}

// stripJSONFence removes a single surrounding Markdown code fence
// (```json … ``` or ``` … ```) from output so a fenced JSON value still
// validates. Input without a fence is returned trimmed but otherwise
// unchanged. Only a fence that wraps the whole value is stripped; a
// fence in the middle of prose is left alone (that output isn't valid
// JSON either way and still fails the check).
func stripJSONFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	// Drop the opening fence line (```, ```json, ```JSON, etc.).
	rest := strings.TrimPrefix(trimmed, "```")
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return trimmed // opening fence with no body; nothing to strip.
	}
	if strings.ContainsAny(strings.TrimSpace(rest[:nl]), " \t") {
		return trimmed // language tag with spaces isn't a fence we recognise.
	}
	body := rest[nl+1:]
	end := strings.LastIndex(body, "```")
	if end < 0 {
		return trimmed // no closing fence; leave as-is.
	}
	if strings.TrimSpace(body[end+3:]) != "" {
		return trimmed // closing fence must wrap the entire response.
	}
	return strings.TrimSpace(body[:end])
}
