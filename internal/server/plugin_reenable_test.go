package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

func TestPluginReenableReusesCurrentApproval(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", `{}`, 200)
	f.call(t, "POST", base+"/configuration", `{"secrets":{"token":"working"}}`, 200)
	var current state.PluginRegistration
	read := func(body string) {
		t.Helper()
		current = state.PluginRegistration{}
		if err := json.Unmarshal([]byte(body), &current); err != nil {
			t.Fatal(err)
		}
	}
	resume := func() string { return `{"approval":"` + current.Approval + `"}` }
	read(f.call(t, "POST", base+"/grants", `{"grants":["context.owner"]}`, 200))
	if current.CanReenable {
		t.Fatal("granting scopes advertised re-enable for a new installation")
	}
	f.call(t, "POST", base+"/enable", resume(), 409) // Granting scopes alone does not approve the executable.
	f.call(t, "POST", base+"/enable", f.approval(t), 200)
	read(f.call(t, "POST", base+"/disable", `{}`, 200))
	if !current.CanReenable {
		t.Fatal("disabling did not retain approval")
	}
	f.call(t, "POST", "/rescan", `{}`, 200)
	read(f.call(t, "POST", base+"/enable", resume(), 200))
	if !current.Enabled || len(current.Grants) != 1 || current.Grants[0] != "context.owner" {
		t.Fatalf("re-enabled registration: %+v", current)
	}
	f.call(t, "POST", base+"/disable", `{}`, 200)
	f.call(t, "POST", base+"/grants", `{"grants":[]}`, 200)
	f.call(t, "POST", base+"/enable", resume(), 409)
	if err := f.s.stateDB.DiscoverPlugin(t.Context(), current.Description, current.ExecutablePath, strings.Repeat("a", 64), nil); err != nil {
		t.Fatal(err)
	}
	read(f.call(t, "POST", base+"/grants", `{"grants":["context.owner"]}`, 200))
	if current.CanReenable {
		t.Fatal("changed executable inherited approval")
	}
	f.call(t, "POST", base+"/enable", resume(), 409) // A new fingerprint and restored scopes are not a new executable approval.
}
