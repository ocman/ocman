package pluginsmod

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPluginConfigurationSchema(t *testing.T) {
	p := state.PluginRegistration{Description: plugins.Description{Settings: []plugins.Setting{
		{Key: "count", Type: "integer", Required: true},
		{Key: "optional", Type: "boolean"},
		{Key: "token", Type: "string", Secret: true, Enum: []string{"allowed"}},
	}}}
	for _, tt := range []struct {
		values, secrets string
		valid           bool
	}{
		{`{}`, `{}`, false},
		{`{"count":1.5}`, `{}`, false},
		{`{"count":1}`, `{}`, true},
		{`{"count":1,"optional":false}`, `{"token":"allowed"}`, true},
		{`{"count":1}`, `{"token":"denied"}`, false},
		{`{"count":1}`, `{"token":""}`, true},
	} {
		var input remote.PluginInput
		if err := json.Unmarshal([]byte(tt.values), &input.Values); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tt.secrets), &input.Secrets); err != nil {
			t.Fatal(err)
		}
		values, err := validatePluginConfiguration(p, input)
		if (err == nil) != tt.valid {
			t.Fatalf("%+v: %v", tt, err)
		}
		if _, ok := values["token"]; ok {
			t.Fatal("secret in public values")
		}
	}
}
