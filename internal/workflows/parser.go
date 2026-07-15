package workflows

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

func decodeDefinition(source []byte) (Definition, []byte, string, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return Definition{}, nil, "", fmt.Errorf("invalid workflow source: %w", err)
	}
	if len(document.Content) != 1 {
		return Definition{}, nil, "", fmt.Errorf("invalid workflow source: definition is required")
	}
	if err := rejectUnsafeYAML(document.Content[0]); err != nil {
		return Definition{}, nil, "", err
	}
	if err := rejectCollectorFields(document.Content[0]); err != nil {
		return Definition{}, nil, "", err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != nil && !errors.Is(err, io.EOF) {
		return Definition{}, nil, "", fmt.Errorf("invalid workflow source: %w", err)
	} else if err == nil {
		return Definition{}, nil, "", fmt.Errorf("invalid workflow source: multiple documents are not supported")
	}
	var raw any
	if err := document.Content[0].Decode(&raw); err != nil {
		return Definition{}, nil, "", fmt.Errorf("invalid workflow source: %w", err)
	}
	decoded, err := json.Marshal(raw)
	if err != nil {
		return Definition{}, nil, "", fmt.Errorf("invalid workflow source: %w", err)
	}
	decoderJSON := json.NewDecoder(bytes.NewReader(decoded))
	decoderJSON.DisallowUnknownFields()
	var definition Definition
	if err := decoderJSON.Decode(&definition); err != nil {
		return Definition{}, nil, "", fmt.Errorf("invalid workflow source: %w", err)
	}
	if err := normalizeLeases(&definition); err != nil {
		return Definition{}, nil, "", err
	}
	if definition.Dependencies == nil {
		definition.Dependencies = []Dependency{}
	}
	if len(definition.Triggers) == 0 {
		definition.Triggers = []Trigger{{ID: "manual", Type: TriggerManual}}
	}
	canonical, err := json.Marshal(definition)
	if err != nil {
		return Definition{}, nil, "", fmt.Errorf("encoding workflow definition: %w", err)
	}
	stableYAML, err := encodeDefinitionYAML(definition)
	return definition, canonical, stableYAML, err
}

func rejectCollectorFields(root *yaml.Node) error {
	nodes := mappingValue(root, "nodes")
	if nodes == nil || nodes.Kind != yaml.SequenceNode {
		return nil
	}
	for _, node := range nodes.Content {
		if key := mappingKey(node, "outputs"); key != nil {
			return sourceError(key, "command collectors are no longer supported; write one JSON value to stdout")
		}
		agent := mappingValue(node, "agent")
		if key := mappingKey(agent, "collectors"); key != nil {
			return sourceError(key, "agent collectors are no longer supported; reply with one JSON value")
		}
	}
	return nil
}

func mappingValue(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == name {
			return node.Content[index+1]
		}
	}
	return nil
}

func mappingKey(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == name {
			return node.Content[index]
		}
	}
	return nil
}

func encodeDefinitionYAML(definition Definition) (string, error) {
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(definition); err != nil {
		return "", fmt.Errorf("exporting workflow YAML: %w", err)
	}
	return out.String(), nil
}

func rejectUnsafeYAML(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return sourceError(node, "anchors are not supported")
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!int" && !isJSONInteger(node.Value) {
		return sourceError(node, "integers must use decimal JSON integer syntax")
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Tag != "!!str" {
				return sourceError(key, "mapping keys must be strings")
			}
			if seen[key.Value] {
				return sourceError(key, fmt.Sprintf("duplicate key %q", key.Value))
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := rejectUnsafeYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func isJSONInteger(value string) bool {
	digits := strings.TrimPrefix(value, "-")
	if digits == "0" {
		return true
	}
	if digits == "" || digits[0] == '0' {
		return false
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func sourceError(node *yaml.Node, message string) error {
	return fmt.Errorf("line %d, column %d: %s", node.Line, node.Column, strings.TrimSpace(message))
}
