package configfile

import (
	"reflect"

	"gopkg.in/yaml.v3"
)

func preserveComments(next, old *yaml.Node) {
	if sameNodeValue(next, old) {
		// Retain the complete syntax tree for no-op saves, including sequence
		// comments, mapping order, scalar styles and quote choices.
		*next = *old
		return
	}
	next.HeadComment, next.LineComment, next.FootComment = old.HeadComment, old.LineComment, old.FootComment
	if next.Kind == yaml.MappingNode && old.Kind == yaml.MappingNode {
		for i := 0; i < len(next.Content); i += 2 {
			for j := 0; j < len(old.Content); j += 2 {
				if next.Content[i].Value == old.Content[j].Value {
					preserveComments(next.Content[i], old.Content[j])
					preserveComments(next.Content[i+1], old.Content[j+1])
					break
				}
			}
		}
	}
	if next.Kind == yaml.SequenceNode && old.Kind == yaml.SequenceNode {
		used := make([]bool, len(old.Content))
		for _, item := range next.Content {
			if match := sequenceMatch(item, old.Content, used); match >= 0 {
				preserveComments(item, old.Content[match])
				used[match] = true
			}
		}
	}
}

func sameNodeValue(a, b *yaml.Node) bool {
	var left, right any
	return a.Decode(&left) == nil && b.Decode(&right) == nil && reflect.DeepEqual(left, right)
}

func sequenceMatch(item *yaml.Node, previous []*yaml.Node, used []bool) int {
	for i, old := range previous {
		if !used[i] && sameNodeValue(item, old) {
			return i
		}
	}
	if item.Kind != yaml.MappingNode {
		return -1
	}
	// Named configuration entries can retain comments across edits/reordering.
	// Never guess when an identifier matches more than one surviving entry.
	for _, field := range []string{"id", "title"} {
		identity := lookup(item, field)
		if identity == nil {
			continue
		}
		match := -1
		for i, old := range previous {
			if used[i] || old.Kind != yaml.MappingNode {
				continue
			}
			if key := lookup(old, field); key != nil && sameNodeValue(identity, key) {
				if match >= 0 {
					match = -1
					break
				}
				match = i
			}
		}
		if match >= 0 {
			return match
		}
	}
	return -1
}
