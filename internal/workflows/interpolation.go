package workflows

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var nodeResultReference = regexp.MustCompile(`\$\{nodes\.([^{}]+)\}`)

func isExactNodeResultReference(input string) bool {
	bounds := nodeResultReference.FindStringIndex(input)
	return bounds != nil && bounds[0] == 0 && bounds[1] == len(input)
}

func exactNodeOutputReference(input string) (string, bool) {
	if !isExactNodeResultReference(input) {
		return "", false
	}
	tail := nodeResultReference.FindStringSubmatch(input)[1]
	return strings.CutSuffix(tail, ".output")
}

func interpolateNodeResults(input string, run RunDetail, nodeID string) (string, error) {
	segments, err := nodeResultSegments(input, run, nodeID)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for _, segment := range segments {
		output.WriteString(segment.value)
	}
	return output.String(), nil
}

type nodeResultSegment struct {
	value   string
	literal bool
}

func nodeResultSegments(input string, run RunDetail, nodeID string) ([]nodeResultSegment, error) {
	if !strings.Contains(input, "${nodes") {
		return []nodeResultSegment{{value: input, literal: true}}, nil
	}
	if strings.Contains(nodeResultReference.ReplaceAllString(input, ""), "${nodes") {
		return nil, fmt.Errorf("malformed node result reference")
	}
	ancestors := transitiveDependencies(run.Version.Definition, nodeID)
	results := make(map[string]NodeResult, len(run.Nodes))
	for _, node := range run.Nodes {
		results[node.NodeID] = node.Result
	}
	var segments []nodeResultSegment
	position := 0
	for _, bounds := range nodeResultReference.FindAllStringIndex(input, -1) {
		if bounds[0] > position {
			segments = append(segments, nodeResultSegment{value: input[position:bounds[0]], literal: true})
		}
		reference := input[bounds[0]:bounds[1]]
		tail := nodeResultReference.FindStringSubmatch(reference)[1]
		if strings.HasSuffix(tail, ".") {
			return nil, fmt.Errorf("malformed node result reference %q", reference)
		}
		referencedID, path := referencedNode(tail, ancestors)
		if referencedID == "" {
			return nil, fmt.Errorf("%q does not identify a dependency of %q", tail, nodeID)
		}
		result, ok := results[referencedID]
		if !ok {
			return nil, fmt.Errorf("node %q has no result", referencedID)
		}
		value, err := nodeResultValue(result, path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", reference, err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		segments = append(segments, nodeResultSegment{value: string(raw)})
		position = bounds[1]
	}
	if position < len(input) {
		segments = append(segments, nodeResultSegment{value: input[position:], literal: true})
	}
	return segments, nil
}

func referencedNode(reference string, ancestors map[string]bool) (string, string) {
	var match string
	for id := range ancestors {
		if (reference == id || strings.HasPrefix(reference, id+".")) && len(id) > len(match) {
			match = id
		}
	}
	return match, strings.TrimPrefix(strings.TrimPrefix(reference, match), ".")
}

func transitiveDependencies(definition Definition, nodeID string) map[string]bool {
	incoming := make(map[string][]string)
	for _, dependency := range definition.Dependencies {
		incoming[dependency.To] = append(incoming[dependency.To], dependency.From)
	}
	ancestors := make(map[string]bool)
	queue := append([]string(nil), incoming[nodeID]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if ancestors[id] {
			continue
		}
		ancestors[id] = true
		queue = append(queue, incoming[id]...)
	}
	return ancestors
}

func nodeResultValue(result NodeResult, path string) (any, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if path == "" {
		return value, nil
	}
	for _, segment := range strings.Split(path, ".") {
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[segment]
			if !ok {
				return nil, fmt.Errorf("field %q does not exist", segment)
			}
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(current) {
				return nil, fmt.Errorf("array index %q does not exist", segment)
			}
			value = current[index]
		default:
			return nil, fmt.Errorf("cannot select %q from %T", segment, value)
		}
	}
	return value, nil
}
