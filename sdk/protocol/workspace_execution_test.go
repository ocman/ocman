package protocol

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWorkspaceActionsNeverNeedProjectIdentity(t *testing.T) {
	d := pageDescription()
	d.Capabilities = []Capability{ActionCapability}
	d.Pages = nil
	d.RequestedGrants = []string{"context.workspace"}
	d.Actions = []ActionDescriptor{{ID: "run", Label: "Run", Placement: "plugin", RequiredGrants: []string{"context.workspace"}, Surfaces: []string{"command-palette"}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	context := ActionContext{WorkspaceID: "workspace-a", ProjectID: "not-an-owner"}
	minimized := d.Actions[0].Minimize(context)
	if minimized.WorkspaceID != "workspace-a" || minimized.ProjectID != "" {
		t.Fatal("workspace context includes project ownership")
	}
	req := ActionRequest{PluginID: d.ID, ActionID: "run", OperationID: "run-1", Placement: "plugin", Surface: "command-palette", Context: minimized}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	req.Context.WorkspaceID = ""
	if req.Validate() == nil {
		t.Fatal("workspace action admitted without workspace identity")
	}
	d.Scope = ScopeHub
	if d.Validate() == nil {
		t.Fatal("owner-local workspace action declared on hub")
	}
}

func TestPluginWorkspaceSessionsArePrivateToolDeniedAndGrantChecked(t *testing.T) {
	d := pageDescription()
	d.Pages = nil
	d.Capabilities = []Capability{PluginSessionCapability}
	d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	grants := []string{SessionRunGrant, SessionPluginGrant}
	scratch := t.TempDir()
	started := 0
	b := NewSessionBroker(func(ctx context.Context, id string, use func(Description, []string, Config) error) error {
		return use(d, grants, Config{Project: "/must-not-use-configured-project"})
	}, func(_ context.Context, _ string, c Config, p SessionRunParams, dir, _ string) (string, error) {
		started++
		if dir != scratch || c.Project != scratch || p.ProjectID != "" || !p.NoTools {
			t.Fatal("model session inherited code-project ownership or tools")
		}
		return "model-turn", nil
	}, nil)
	b.ResolveProject = func(context.Context, string) (string, error) {
		t.Fatal("workspace session resolved a project")
		return "", nil
	}
	b.ResolveWorkspace = func(_ context.Context, pluginID, workspaceID string) (string, error) {
		if pluginID != d.ID || workspaceID != "workspace-a" {
			t.Fatal("wrong private workspace identity")
		}
		return scratch, nil
	}
	run := func(p SessionRunParams, version Version) error {
		raw, _ := json.Marshal(p)
		_, err := b.Run(t.Context(), d.ID, Call{OperationID: "model", Capability: "session", Version: version, Method: "run", Params: raw})
		return err
	}
	params := SessionRunParams{WorkspaceID: "workspace-a", NoTools: true, Prompt: "verify finding"}
	if err := run(params, PluginSessionCapability.Version); err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatal("private model session did not start")
	}
	for _, bad := range []SessionRunParams{
		{WorkspaceID: "workspace-a", Prompt: "no tools flag"},
		{WorkspaceID: "workspace-a", NoTools: true, ProjectID: "project-a", Prompt: "mixed context"},
		{WorkspaceID: "workspace-a", NoTools: true, Subpath: "../escape", Prompt: "escape"},
		{NoTools: true, Prompt: "configured-project fallback"},
	} {
		if run(bad, PluginSessionCapability.Version) == nil {
			t.Fatal("unsafe workspace model session admitted")
		}
	}
	if run(params, ProjectSessionCapability.Version) == nil {
		t.Fatal("workspace session admitted on 1.1")
	}
	grants = []string{SessionRunGrant}
	if run(params, PluginSessionCapability.Version) == nil {
		t.Fatal("revoked workspace-session grant admitted")
	}
	if started != 1 {
		t.Fatal("refused request reached session creation")
	}
}
