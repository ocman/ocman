package plugins

import (
	"encoding/json"
	"math"
	"regexp"
)

// Setting is the host-rendered v1 schema. Secrets are string-only and may not
// declare defaults. Enum is an optional set of allowed string values.
type Setting struct {
	Key      string          `json:"key"`
	Type     string          `json:"type"`
	Label    string          `json:"label"`
	Required bool            `json:"required,omitempty"`
	Secret   bool            `json:"secret,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
	Enum     []string        `json:"enum,omitempty"`
}

var settingKey = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,127}$`)

func validateSettingsAndGrants(settings []Setting, grants []string) error {
	seen := make(map[string]bool)
	for _, grant := range grants {
		if !settingKey.MatchString(grant) || seen[grant] {
			return ErrInvalidMessage
		}
		seen[grant] = true
	}
	clear(seen)
	for _, s := range settings {
		if !settingKey.MatchString(s.Key) || seen[s.Key] || !validText(s.Label) ||
			(s.Secret && (s.Type != "string" || len(s.Default) != 0)) {
			return ErrInvalidMessage
		}
		seen[s.Key] = true
		switch s.Type {
		case "string", "boolean", "number", "integer":
		default:
			return ErrInvalidMessage
		}
		options := make(map[string]bool)
		for _, option := range s.Enum {
			if s.Type != "string" || !validText(option) || options[option] {
				return ErrInvalidMessage
			}
			options[option] = true
		}
		if len(s.Default) != 0 && !s.Accepts(s.Default) {
			return ErrInvalidMessage
		}
	}
	return nil
}

// Accepts checks a configuration value without returning the value in an error.
func (s Setting) Accepts(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch s.Type {
	case "string":
		v, ok := value.(string)
		if !ok {
			return false
		}
		if len(s.Enum) == 0 {
			return true
		}
		for _, option := range s.Enum {
			if option == v {
				return true
			}
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		v, ok := value.(float64)
		return ok && (s.Type == "number" || math.Trunc(v) == v)
	}
	return false
}
