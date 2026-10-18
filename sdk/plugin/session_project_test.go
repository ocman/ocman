package plugin

import (
	"encoding/json"
	"testing"
)

func TestNewProjectSessionRunSelectsExtension(t *testing.T) {
	for _, params := range []SessionRunParams{{Prompt: "judge", ProjectID: "project-a", NoTools: true}, {Prompt: "judge", NoTools: true}} {
		call, err := NewSessionRun("operation", params)
		if err != nil || call.Version != ProjectSessionCapability.Version {
			t.Fatalf("project session version: %+v %v", call, err)
		}
		var decoded SessionRunParams
		if json.Unmarshal(call.Params, &decoded) != nil || decoded != params {
			t.Fatal("project session parameters changed")
		}
	}
	if _, err := NewSessionRun("operation", SessionRunParams{Prompt: "judge", ProjectID: "../escape"}); err == nil {
		t.Fatal("path masquerading as project ID accepted")
	}
}
