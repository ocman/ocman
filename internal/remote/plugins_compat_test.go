package remote

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

func TestLegacyPluginRequestsOmitPaneEnvelope(t *testing.T) {
	// Prior remotes use a strict decoder and still speak protocol version 7.
	// Freeze their top-level request shape so additive fields cannot break it.
	for _, operation := range []string{"available", "catalog", "configuration", "invoke"} {
		t.Run(operation, func(t *testing.T) {
			data, err := json.Marshal(PluginRequest{Operation: operation, PluginID: "org.example.plugin"})
			if err != nil {
				t.Fatal(err)
			}
			var legacy struct {
				Operation string                `json:"operation"`
				PluginID  string                `json:"pluginId,omitempty"`
				Read      bool                  `json:"read,omitempty"`
				Input     PluginInput           `json:"input,omitempty"`
				Action    plugins.ActionRequest `json:"action,omitempty"`
				Handle    string                `json:"handle,omitempty"`
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&legacy); err != nil {
				t.Fatalf("old remote rejected %s: %v", data, err)
			}
			if legacy.Operation != operation {
				t.Fatal(legacy.Operation)
			}
		})
	}
}

func TestPaneRequestsKeepNonzeroEnvelope(t *testing.T) {
	request := PluginRequest{Operation: "panes", Pane: plugins.PaneRequest{OwnerID: "machine"}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PluginRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Pane.OwnerID != "machine" {
		t.Fatalf("missing pane envelope: %s", data)
	}
}
