package configfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const maxSize = 4 << 20

// Document keeps the syntax tree so edits retain comments and unrelated fields.
type Document struct {
	root yaml.Node
}

func Parse(data []byte) (*Document, error) {
	if len(data) > maxSize {
		return nil, errors.New("configuration exceeds size limit")
	}
	d := &Document{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&d.root); err != nil {
		return nil, errors.New("invalid configuration YAML")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("configuration must contain exactly one YAML document")
	}
	if len(d.root.Content) != 1 || d.root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("configuration must be a mapping")
	}
	if err := validateNode(&d.root, 0); err != nil {
		return nil, err
	}
	root := d.root.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		switch root.Content[i].Value {
		case "version", "startup", "settings":
		default:
			return nil, errors.New("unknown top-level configuration field")
		}
	}
	v := lookup(root, "version")
	if v == nil || v.Tag != "!!int" || v.Value != "1" {
		return nil, errors.New("unsupported configuration version; expected version: 1")
	}
	for _, section := range []string{"startup", "settings"} {
		if n := lookup(root, section); n != nil && n.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s must be a mapping", section)
		}
	}
	if startup := lookup(root, "startup"); startup != nil {
		for i := 1; i < len(startup.Content); i += 2 {
			if startup.Content[i].Kind != yaml.ScalarNode || startup.Content[i].Tag == "!!null" {
				return nil, errors.New("startup options must be scalar values")
			}
		}
	}
	return d, nil
}

func validateNode(n *yaml.Node, depth int) error {
	if depth > 32 || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return errors.New("configuration aliases, anchors, or excessive nesting are unsupported")
	}
	if n.Kind == yaml.ScalarNode {
		switch n.Tag {
		case "!!str", "!!int", "!!bool", "!!float", "!!null":
		default:
			return errors.New("unsupported configuration scalar type")
		}
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Tag != "!!str" || seen[key.Value] {
				return errors.New("configuration keys must be unique strings")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := validateNode(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func lookup(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func (d *Document) Keys(section string) []string {
	var keys []string
	if n := lookup(d.root.Content[0], section); n != nil {
		for i := 0; i < len(n.Content); i += 2 {
			keys = append(keys, n.Content[i].Value)
		}
	}
	return keys
}

// Get adapts YAML values to the existing string/JSON settings interfaces.
func (d *Document) Get(section, key string) (string, bool, error) {
	n := lookup(lookup(d.root.Content[0], section), key)
	if n == nil {
		return "", false, nil
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		return n.Value, true, nil
	}
	var value any
	if err := n.Decode(&value); err != nil {
		return "", false, errors.New("cannot decode configuration value")
	}
	b, err := json.Marshal(value)
	if err != nil {
		return "", false, errors.New("cannot encode configuration value")
	}
	return string(b), true, nil
}

// Set accepts decoded values, not pre-serialized YAML.
func (d *Document) Set(section, key string, value any) error {
	var n yaml.Node
	if err := n.Encode(value); err != nil {
		return errors.New("cannot encode configuration value")
	}
	root := d.root.Content[0]
	mapping := lookup(root, section)
	if mapping == nil {
		mapping = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: section}, mapping)
	}
	if old := lookup(mapping, key); old != nil {
		preserveComments(&n, old)
		*old = n
	} else {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
	}
	return nil
}

func (d *Document) Bytes() ([]byte, error) {
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(&d.root); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (d *Document) Delete(section, key string) {
	mapping := lookup(d.root.Content[0], section)
	if mapping == nil {
		return
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}
