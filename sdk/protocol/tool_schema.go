package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxToolSchemaDepth = 16
	maxToolSchemaNodes = 512
)

var toolSchemaTypes = []string{"object", "array", "string", "integer", "number", "boolean"}

// toolSchema is a compiled node of the closed tool.v1 JSON Schema subset.
type toolSchema struct {
	Type         string
	Properties   map[string]*toolSchema
	Required     []string
	NoAdditional bool
	Additional   *toolSchema
	Items        *toolSchema
	MinItems     *int
	MaxItems     *int
	Enum         []any
	Const        any
	HasConst     bool
	MinLength    *int
	MaxLength    *int
	Minimum      *big.Rat
	Maximum      *big.Rat
	WriteOnly    bool
}

// ToolArgumentError names the JSON path and the rule an argument failed.
type ToolArgumentError struct {
	Path   string
	Reason string
}

func (e *ToolArgumentError) Error() string { return e.Path + ": " + e.Reason }

func decodeJSONNumbers(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, ErrInvalidMessage
	}
	return v, nil
}

func compileToolSchema(raw json.RawMessage) (*toolSchema, error) {
	if len(raw) == 0 || checkJSON(raw) != nil {
		return nil, ErrInvalidMessage
	}
	v, err := decodeJSONNumbers(raw)
	if err != nil {
		return nil, ErrInvalidMessage
	}
	nodes := 0
	return compileToolSchemaNode(v, 0, &nodes)
}

func nonNegative(v any) (*int, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return nil, false
	}
	i, err := strconv.Atoi(n.String())
	if err != nil || i < 0 {
		return nil, false
	}
	return &i, true
}

func rational(v any) (*big.Rat, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return nil, false
	}
	return parseNumber(n)
}

// parseNumber bounds the text and exponent first: an exact rational for
// "1e999999999" would take unbounded time and memory.
func parseNumber(n json.Number) (*big.Rat, bool) {
	s := n.String()
	if len(s) > 64 {
		return nil, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(strings.TrimPrefix(s[i+1:], "+"))
		if err != nil || exponent > 400 || exponent < -400 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
}

func compileToolSchemaNode(v any, depth int, nodes *int) (*toolSchema, error) {
	*nodes++
	object, ok := v.(map[string]any)
	if !ok || depth > maxToolSchemaDepth || *nodes > maxToolSchemaNodes {
		return nil, ErrInvalidMessage
	}
	s := &toolSchema{}
	for _, key := range slices.Sorted(maps.Keys(object)) {
		value := object[key]
		var good bool
		switch key {
		case "type":
			s.Type, good = value.(string)
			good = good && slices.Contains(toolSchemaTypes, s.Type)
		case "properties":
			var props map[string]any
			props, good = value.(map[string]any)
			s.Properties = map[string]*toolSchema{}
			for _, name := range slices.Sorted(maps.Keys(props)) {
				child, err := compileToolSchemaNode(props[name], depth+1, nodes)
				if err != nil || name == "" || !utf8.ValidString(name) {
					return nil, ErrInvalidMessage
				}
				s.Properties[name] = child
			}
		case "required":
			var list []any
			list, good = value.([]any)
			for _, item := range list {
				name, isString := item.(string)
				if !isString || slices.Contains(s.Required, name) {
					return nil, ErrInvalidMessage
				}
				s.Required = append(s.Required, name)
			}
		case "additionalProperties":
			if allowed, isBool := value.(bool); isBool {
				s.NoAdditional, good = !allowed, true
				break
			}
			child, err := compileToolSchemaNode(value, depth+1, nodes)
			s.Additional, good = child, err == nil
		case "items":
			child, err := compileToolSchemaNode(value, depth+1, nodes)
			s.Items, good = child, err == nil
		case "minItems":
			s.MinItems, good = nonNegative(value)
		case "maxItems":
			s.MaxItems, good = nonNegative(value)
		case "minLength":
			s.MinLength, good = nonNegative(value)
		case "maxLength":
			s.MaxLength, good = nonNegative(value)
		case "minimum":
			s.Minimum, good = rational(value)
		case "maximum":
			s.Maximum, good = rational(value)
		case "enum":
			s.Enum, good = value.([]any)
			good = good && len(s.Enum) > 0
		case "const":
			s.Const, s.HasConst, good = value, true, true
		case "title", "description":
			_, good = value.(string)
		case "writeOnly":
			s.WriteOnly, good = value.(bool)
		case "examples":
			_, good = value.([]any)
		case "default":
			good = true
		}
		if !good {
			return nil, ErrInvalidMessage
		}
	}
	if depth == 0 && s.Type == "" {
		return nil, ErrInvalidMessage
	}
	return s, nil
}

// ValidateToolArguments validates args against a tool.v1 schema. A schema
// outside the subset returns ErrInvalidMessage; a failing argument returns a
// *ToolArgumentError rooted at "arguments".
func ValidateToolArguments(schema, args json.RawMessage) error {
	s, err := compileToolSchema(schema)
	if err != nil {
		return ErrInvalidMessage
	}
	if len(args) > MaxToolArgumentBytes {
		return &ToolArgumentError{Path: "arguments", Reason: "must be at most " + strconv.Itoa(MaxToolArgumentBytes) + " bytes"}
	}
	if checkJSON(args) != nil {
		return &ToolArgumentError{Path: "arguments", Reason: "must be valid JSON without duplicate keys"}
	}
	v, err := decodeJSONNumbers(args)
	if err != nil {
		return &ToolArgumentError{Path: "arguments", Reason: "must be valid JSON"}
	}
	if _, ok := v.(map[string]any); !ok {
		return &ToolArgumentError{Path: "arguments", Reason: "must be an object"}
	}
	return s.validate("arguments", v)
}

func jsonType(v any) string {
	switch x := v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		r, ok := parseNumber(x)
		if !ok {
			return "unsupported number"
		}
		if r.IsInt() {
			return "integer"
		}
		return "number"
	}
	return "null"
}

func typeMatches(want string, v any) bool {
	got := jsonType(v)
	return want == "" || got == want || want == "number" && got == "integer"
}

func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		rx, okx := parseNumber(x)
		ry, oky := parseNumber(y)
		return okx && oky && rx.Cmp(ry) == 0
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, found := y[k]
			if !found || !jsonEqual(xv, yv) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		return ok && slices.EqualFunc(x, y, jsonEqual)
	default:
		return a == b
	}
}

func (s *toolSchema) validate(path string, v any) error {
	fail := func(format string, args ...any) error {
		return &ToolArgumentError{Path: path, Reason: fmt.Sprintf(format, args...)}
	}
	if !typeMatches(s.Type, v) {
		return fail("must be of type %s", s.Type)
	}
	if s.HasConst && !jsonEqual(s.Const, v) {
		return fail("must equal the declared constant")
	}
	if len(s.Enum) > 0 && !slices.ContainsFunc(s.Enum, func(e any) bool { return jsonEqual(e, v) }) {
		return fail("must be one of the declared values")
	}
	switch x := v.(type) {
	case string:
		n := utf8.RuneCountInString(x)
		if s.MinLength != nil && n < *s.MinLength {
			return fail("must be at least %d characters", *s.MinLength)
		}
		if s.MaxLength != nil && n > *s.MaxLength {
			return fail("must be at most %d characters", *s.MaxLength)
		}
	case json.Number:
		r, ok := parseNumber(x)
		if !ok {
			return fail("must be a number of at most 64 characters with an exponent within ±400")
		}
		if s.Minimum != nil && r.Cmp(s.Minimum) < 0 {
			return fail("must be at least %s", s.Minimum.RatString())
		}
		if s.Maximum != nil && r.Cmp(s.Maximum) > 0 {
			return fail("must be at most %s", s.Maximum.RatString())
		}
	case []any:
		if s.MinItems != nil && len(x) < *s.MinItems {
			return fail("must have at least %d items", *s.MinItems)
		}
		if s.MaxItems != nil && len(x) > *s.MaxItems {
			return fail("must have at most %d items", *s.MaxItems)
		}
		if s.Items != nil {
			for i, item := range x {
				if err := s.Items.validate(path+"["+strconv.Itoa(i)+"]", item); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		for _, name := range s.Required {
			if _, ok := x[name]; !ok {
				return &ToolArgumentError{Path: path + "." + name, Reason: "is required"}
			}
		}
		for _, name := range slices.Sorted(maps.Keys(x)) {
			child, declared := s.Properties[name]
			switch {
			case declared:
			case s.Additional != nil:
				child = s.Additional
			case s.NoAdditional:
				return &ToolArgumentError{Path: path + "." + name, Reason: "is not an allowed property"}
			default:
				continue
			}
			if err := child.validate(path+"."+name, x[name]); err != nil {
				return err
			}
		}
	}
	return nil
}

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
	raw, err := json.Marshal(s.mask(v))
	if err != nil {
		return nil
	}
	return raw
}

const maskedToolValue = "(hidden)"

func (s *toolSchema) mask(v any) any {
	if s == nil {
		return v
	}
	if s.WriteOnly {
		return maskedToolValue
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			child := s.Properties[k]
			if child == nil {
				child = s.Additional
			}
			out[k] = child.mask(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = s.Items.mask(item)
		}
		return out
	}
	return v
}

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
