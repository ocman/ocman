package protocol

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWorkspaceSessionProjectAssociationDoesNotChangeExecution(t *testing.T) {
	d := pageDescription()
	d.Pages = nil
	d.Capabilities = []Capability{{Name: "session", Version: Version{Major: 1, Minor: 6}}}
	d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant, SessionAssociateGrant}
	grants := append([]string(nil), d.RequestedGrants...)
	root := t.TempDir()
	started, resolved := 0, 0
	b := NewSessionBroker(func(_ context.Context, _ string, use func(Description, []string, Config) error) error {
		return use(d, grants, Config{})
	}, func(_ context.Context, _ string, c Config, _ SessionRunParams, dir, _ string) (string, error) {
		started++
		if dir != root || c.Project != root {
			t.Fatalf("association changed execution: %s %+v", dir, c)
		}
		if c.AssociatedProject != "/projects/platform-service" {
			t.Fatalf("resolved display association lost: %+v", c)
		}
		return "source-session", nil
	}, nil)
	b.ResolveWorkspace = func(context.Context, string, string) (string, error) { return root, nil }
	b.ResolveProject = func(_ context.Context, id string) (string, error) {
		resolved++
		if id != "platform-service" {
			t.Fatalf("unexpected project lookup: %s", id)
		}
		return "/projects/platform-service", nil
	}
	run := func(minor int) error {
		_, err := b.Run(t.Context(), d.ID, Call{OperationID: "associated", Capability: "session", Version: Version{Major: 1, Minor: minor}, Method: "run", Params: json.RawMessage(`{"workspaceId":"workspace-a","associatedProjectId":"platform-service","noTools":true,"prompt":"Review"}`)})
		return err
	}
	if err := run(6); err != nil {
		t.Fatalf("project-associated workspace run rejected: %v", err)
	}
	if started != 1 || resolved != 1 {
		t.Fatalf("started=%d resolved=%d", started, resolved)
	}
	if run(5) == nil {
		t.Fatal("old minor accepted project association")
	}
	grants = []string{SessionRunGrant, SessionPluginGrant}
	if run(6) == nil || started != 1 {
		t.Fatal("association bypassed its grant")
	}
	grants = append(grants, SessionAssociateGrant)
	for _, lookup := range []func(context.Context, string) (string, error){
		nil,
		func(context.Context, string) (string, error) { return "relative", nil },
		func(context.Context, string) (string, error) { return "", ErrUnavailable },
	} {
		b.ResolveProject = lookup
		if run(6) == nil || started != 1 {
			t.Fatal("unavailable owner project accepted")
		}
	}
	b.ResolveProject = func(context.Context, string) (string, error) { return "/projects/platform-service", nil }
	d.Capabilities[0].Version.Minor = 5
	if run(6) == nil {
		t.Fatal("undeclared association minor accepted")
	}
	d.Capabilities[0].Version.Minor = 6
	d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant}
	if run(6) == nil {
		t.Fatal("undeclared association grant accepted")
	}
}

func TestAssociationGrantDoesNotPermitProjectExecution(t *testing.T) {
	d := sessionDescription()
	d.Settings = nil
	d.Capabilities = []Capability{AssociatedSessionCapability}
	d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant, SessionAssociateGrant}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	h := &sessionHarness{description: d, grants: d.RequestedGrants}
	b := h.broker()
	b.ResolveProject = func(context.Context, string) (string, error) { return "/projects/platform-service", nil }
	call := sessionRunEvent(t, SessionRunParams{ProjectID: "platform-service", Prompt: "must not execute"})
	call.Version = AssociatedSessionCapability.Version
	if _, err := b.Run(t.Context(), d.ID, call); err == nil || len(h.started) != 0 {
		t.Fatal("display association grant authorized project execution")
	}
	for _, tc := range []struct {
		minor     int
		workspace bool
	}{{5, true}, {6, false}} {
		d.Capabilities[0].Version.Minor = tc.minor
		d.RequestedGrants = []string{SessionRunGrant, SessionAssociateGrant}
		if tc.workspace {
			d.RequestedGrants = append(d.RequestedGrants, SessionPluginGrant)
		}
		if d.Validate() == nil {
			t.Fatalf("association grant accepted without workspace 1.6: %+v", tc)
		}
	}
}

func TestSessionAssociationRequiresWorkspaceAndOpaqueProjectID(t *testing.T) {
	for _, raw := range []string{
		`{"associatedProjectId":"project-a","prompt":"missing workspace"}`,
		`{"workspaceId":"workspace-a","noTools":true,"associatedProjectId":"/projects/a","prompt":"raw path"}`,
		`{"workspaceId":"workspace-a","projectId":"project-a","noTools":true,"associatedProjectId":"project-a","prompt":"execution override"}`,
	} {
		if _, err := DecodeSessionRunParams(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid association accepted: %s", raw)
		}
	}
}
