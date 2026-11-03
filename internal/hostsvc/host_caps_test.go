package hostsvc

import (
	"encoding/json"
	"testing"
)

func TestHostCapsLaunchWireCompatibility(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"sessionLaunch":true}`, true},
		{`{"opencodeLaunch":true}`, true},
		{`{"sessionLaunch":false,"opencodeLaunch":true}`, false},
		{`{"gitDiff":true}`, false},
	} {
		var caps HostCaps
		if err := json.Unmarshal([]byte(tc.body), &caps); err != nil {
			t.Fatal(err)
		}
		if caps.SessionLaunch != tc.want {
			t.Errorf("%s: SessionLaunch = %v, want %v", tc.body, caps.SessionLaunch, tc.want)
		}
	}
	var caps HostCaps
	if err := json.Unmarshal([]byte(`{"gitDiff":true,"mcpConfig":true}`), &caps); err != nil || !caps.GitDiff || !caps.McpConfig {
		t.Fatalf("other fields lost: %+v %v", caps, err)
	}
	if err := json.Unmarshal([]byte(`[]`), &caps); err == nil {
		t.Fatal("invalid JSON accepted")
	}

	data, err := json.Marshal(HostCaps{SessionLaunch: true}.WithLegacyFields())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil || wire["sessionLaunch"] != true || wire["opencodeLaunch"] != true {
		t.Fatalf("remote wire = %s %v", data, err)
	}
	var round HostCaps
	if err := json.Unmarshal(data, &round); err != nil || !round.SessionLaunch {
		t.Fatalf("round trip = %+v %v", round, err)
	}
}
