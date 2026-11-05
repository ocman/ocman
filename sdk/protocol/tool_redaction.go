package protocol

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// MaskToolArguments replaces values declared writeOnly with a fixed marker,
// for showing arguments to a user. Invalid input yields nil.
func MaskToolArguments(schema, args json.RawMessage) json.RawMessage {
	s, err := compileToolSchema(schema)
	if err != nil {
		return nil
	}
	v, err := decodeJSONNumbers(args)
	if err != nil {
		return nil
	}
	var pointers []string
	s.writeOnlyPointers("", v, &pointers)
	for _, pointer := range pointers {
		v = redactPointer(v, pointer)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

const maskedToolValue = "(hidden)"

// ToolWriteOnlyPointers lists JSON Pointers to the values in args that schema
// declares writeOnly, for a confirmation request's Redact. Invalid input
// yields nil.
func ToolWriteOnlyPointers(schema, args json.RawMessage) []string {
	s, err := compileToolSchema(schema)
	if err != nil {
		return nil
	}
	v, err := decodeJSONNumbers(args)
	if err != nil {
		return nil
	}
	var pointers []string
	s.writeOnlyPointers("", v, &pointers)
	return pointers
}

func escapePointer(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

func (s *toolSchema) writeOnlyPointers(prefix string, v any, out *[]string) {
	if s == nil {
		return
	}
	if s.WriteOnly {
		*out = append(*out, prefix)
		return
	}
	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			child := s.Properties[k]
			if child == nil {
				child = s.Additional
			}
			child.writeOnlyPointers(prefix+"/"+escapePointer(k), x[k], out)
		}
	case []any:
		for i, item := range x {
			s.Items.writeOnlyPointers(prefix+"/"+strconv.Itoa(i), item, out)
		}
	}
}

// RedactToolArguments hides the values at the given JSON Pointers. Pointers
// that resolve to nothing are ignored; redaction can only hide values.
func RedactToolArguments(args json.RawMessage, pointers []string) json.RawMessage {
	v, err := decodeJSONNumbers(args)
	if err != nil {
		return nil
	}
	for _, pointer := range pointers {
		v = redactPointer(v, pointer)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

func redactPointer(v any, pointer string) any {
	if pointer == "" {
		return maskedToolValue
	}
	if !strings.HasPrefix(pointer, "/") {
		return v
	}
	token, rest, nested := strings.Cut(pointer[1:], "/")
	if nested {
		rest = "/" + rest
	}
	token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	switch x := v.(type) {
	case map[string]any:
		if child, ok := x[token]; ok {
			x[token] = redactPointer(child, rest)
		}
	case []any:
		if i, err := strconv.Atoi(token); err == nil && i >= 0 && i < len(x) {
			x[i] = redactPointer(x[i], rest)
		}
	}
	return v
}
