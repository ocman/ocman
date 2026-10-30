package protocol

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReadOnlyWorkspaceSessionRequiresVersionAndSourceGrant(t *testing.T) {
	d := pageDescription()
	d.Pages = nil
	d.Capabilities = []Capability{{Name: "session", Version: Version{Major: 1, Minor: 4}}}
	d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant, "session.source"}
	grants := append([]string(nil), d.RequestedGrants...)
	root := t.TempDir()
	started := 0
	b := NewSessionBroker(func(_ context.Context, _ string, use func(Description, []string, Config) error) error {
		return use(d, grants, Config{})
	}, func(_ context.Context, _ string, _ Config, _ SessionRunParams, dir, _ string) (string, error) {
		started++
		if dir != root+"/documents" {
			t.Fatalf("unexpected source directory: %s", dir)
		}
		return "source-session", nil
	}, nil)
	b.ResolveWorkspace = func(context.Context, string, string) (string, error) { return root, nil }
	run := func(raw string, minor int) error {
		_, err := b.Run(t.Context(), d.ID, Call{OperationID: "source", Capability: "session", Version: Version{Major: 1, Minor: minor}, Method: "run", Params: json.RawMessage(raw)})
		return err
	}
	valid := `{"workspaceId":"workspace-a","readOnly":true,"subpath":"documents","prompt":"Summarize these documents"}`
	if err := run(valid, 4); err != nil {
		t.Fatalf("read-only source session unavailable: %v", err)
	}
	for _, raw := range []string{
		`{"workspaceId":"workspace-a","readOnly":true,"prompt":"missing source"}`,
		`{"workspaceId":"workspace-a","readOnly":true,"noTools":true,"subpath":"documents","prompt":"mixed mode"}`,
		`{"workspaceId":"workspace-a","readOnly":true,"subpath":"../outside","prompt":"escape"}`,
		`{"workspaceId":"workspace-a","readOnly":true,"permissionProfile":"unsafe","subpath":"documents","prompt":"override"}`,
		`{"projectId":"project-a","readOnly":true,"prompt":"wrong scope"}`,
	} {
		if run(raw, 4) == nil {
			t.Fatalf("unsafe source run accepted: %s", raw)
		}
	}
	if run(valid, 3) == nil {
		t.Fatal("source mode accepted by old protocol version")
	}
	grants = []string{SessionRunGrant, SessionPluginGrant}
	if SessionAllowed(d, grants) == nil {
		t.Fatal("completion admission ignores revoked source grant")
	}
	if run(valid, 4) == nil {
		t.Fatal("source session admitted without source grant")
	}
	if started != 1 {
		t.Fatalf("refused request created a session: %d", started)
	}
}
