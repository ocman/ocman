package protocol

import (
	"context"
	"testing"
)

func TestSessionBrokerChildRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wire     Version
		declared Capability
		want     ErrorCategory
	}{
		{"old wire", StreamingSessionCapability.Version, ChildSessionCapability, ErrorInvalidArgument},
		{"old declaration", ChildSessionCapability.Version, StreamingSessionCapability, ErrorPermissionDenied},
		{"child admitted", ChildSessionCapability.Version, ChildSessionCapability, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := sessionDescription()
			d.Settings = nil
			d.Capabilities = []Capability{tc.declared}
			d.RequestedGrants = []string{SessionRunGrant, SessionPluginGrant}
			root := t.TempDir()
			params := SessionRunParams{Prompt: "Review", WorkspaceID: "workspace-a", NoTools: true, ParentRun: "parent-run"}
			started := 0
			b := NewSessionBroker(func(_ context.Context, _ string, use func(Description, []string, Config) error) error {
				return use(d, d.RequestedGrants, Config{})
			}, func(_ context.Context, _ string, _ Config, got SessionRunParams, dir, _ string) (string, error) {
				started++
				if got.ParentRun != params.ParentRun || dir != root {
					t.Fatalf("start got parent %q and directory %q, want %q and %q", got.ParentRun, dir, params.ParentRun, root)
				}
				return "child-session", nil
			}, nil)
			b.ResolveWorkspace = func(context.Context, string, string) (string, error) { return root, nil }
			call := sessionRunEvent(t, params)
			call.Version = tc.wire
			result, err := b.Run(t.Context(), d.ID, call)
			if tc.want != "" {
				assertWire(t, err, tc.want)
				if started != 0 {
					t.Fatalf("rejected run started %d sessions", started)
				}
				return
			}
			if err != nil || result.SessionID != "child-session" || started != 1 {
				t.Fatalf("run: %+v, err=%v, starts=%d", result, err, started)
			}
		})
	}
}
