package plugins

import (
	"encoding/json"
	"testing"
)

func TestDescriptionSettingsAndGrants(t *testing.T) {
	for _, tt := range []struct {
		name    string
		setting Setting
		valid   bool
	}{
		{"string", Setting{Type: "string", Default: json.RawMessage(`"hello"`)}, true},
		{"boolean", Setting{Type: "boolean", Default: json.RawMessage(`true`)}, true},
		{"number", Setting{Type: "number", Default: json.RawMessage(`1.5`)}, true},
		{"integer", Setting{Type: "integer", Default: json.RawMessage(`2`)}, true},
		{"secret", Setting{Type: "string", Secret: true}, true},
		{"enum", Setting{Type: "string", Enum: []string{"one", "two"}, Default: json.RawMessage(`"two"`)}, true},
		{"enum mismatch", Setting{Type: "string", Enum: []string{"one"}, Default: json.RawMessage(`"two"`)}, false},
		{"enum duplicate", Setting{Type: "string", Enum: []string{"one", "one"}}, false},
		{"enum empty", Setting{Type: "string", Enum: []string{""}}, false},
		{"enum wrong type", Setting{Type: "number", Enum: []string{"one"}}, false},
		{"unknown type", Setting{Type: "object"}, false},
		{"secret default", Setting{Type: "string", Secret: true, Default: json.RawMessage(`"secret"`)}, false},
		{"secret number", Setting{Type: "number", Secret: true}, false},
		{"null default", Setting{Type: "string", Default: json.RawMessage(`null`)}, false},
		{"invalid json", Setting{Type: "string", Default: json.RawMessage(`invalid`)}, false},
		{"wrong boolean", Setting{Type: "boolean", Default: json.RawMessage(`"true"`)}, false},
		{"wrong number", Setting{Type: "number", Default: json.RawMessage(`"1"`)}, false},
		{"fractional integer", Setting{Type: "integer", Default: json.RawMessage(`1.5`)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := *hello(ModeDescribe).Hello.Description
			tt.setting.Key, tt.setting.Label = "setting", "Setting"
			d.Settings = []Setting{tt.setting}
			d.RequestedGrants = []string{"context.project", "context.session"}
			if err := d.Validate(); (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
	for name, mutate := range map[string]func(*Description){
		"key":             func(d *Description) { d.Settings[0].Key = "../file" },
		"label":           func(d *Description) { d.Settings[0].Label = "" },
		"duplicate key":   func(d *Description) { d.Settings = append(d.Settings, d.Settings[0]) },
		"grant":           func(d *Description) { d.RequestedGrants = []string{"*"} },
		"duplicate grant": func(d *Description) { d.RequestedGrants = []string{"read", "read"} },
	} {
		t.Run(name, func(t *testing.T) {
			d := *hello(ModeDescribe).Hello.Description
			d.Settings = []Setting{{Key: "key", Label: "Label", Type: "string"}}
			mutate(&d)
			if d.Validate() == nil {
				t.Fatal("accepted invalid schema")
			}
		})
	}
}
