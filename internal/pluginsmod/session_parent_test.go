package pluginsmod

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type pluginParentPlatform struct {
	platforms.Platform
	row db.Session
}

func (p pluginParentPlatform) SessionMetadata(context.Context, string) (*db.Session, error) {
	return &p.row, nil
}

func TestPluginChildRunOnlyAttachesToOwnedRootInSameDirectory(t *testing.T) {
	for _, tc := range []struct {
		name                                                      string
		foreign, otherDirectory, nested, unsettled, otherPlatform bool
	}{
		{name: "same owner and directory"},
		{name: "foreign plugin", foreign: true},
		{name: "other directory", otherDirectory: true},
		{name: "nested child", nested: true},
		{name: "unsettled parent", unsettled: true},
		{name: "other platform", otherPlatform: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			root := t.TempDir()
			const pluginID = "org.example.reviewer"
			const parent = "parent-session"
			creator := new(sourceTestSessions)
			create, lookup := localPlatforms("opencode")
			row := db.Session{Directory: root}
			if tc.otherDirectory {
				row.Directory = t.TempDir()
			}
			if tc.nested {
				row.ParentID = "grandparent"
			}
			m := &Module{store: new(state.DB), plugins: openPluginStore(t), deps: Deps{
				Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator, CreatePlatform: create, Platform: lookup,
				Adapter: func(_ context.Context, platform, id string) (platforms.Platform, bool) {
					return pluginParentPlatform{row: row}, platform == "opencode" && id == parent
				},
				Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error { return nil },
			}}
			owner, platform := pluginID, "opencode"
			if tc.foreign {
				owner = "org.example.other"
			}
			if tc.otherPlatform {
				platform = "other"
			}
			if _, claimed, err := m.plugins.reserveSessionRun(ctx, owner, "coordinator", root, plugins.SessionRunParams{Prompt: "Coordinate"}); err != nil || !claimed {
				t.Fatalf("parent reservation: %t %v", claimed, err)
			}
			if !tc.unsettled {
				if err := m.plugins.finishSessionRun(ctx, owner, "coordinator", parent, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.plugins.LinkPluginSession(ctx, platform, parent, owner); err != nil {
				t.Fatal(err)
			}
			params := plugins.SessionRunParams{WorkspaceID: "workspace", ParentRun: "coordinator", NoTools: true, Prompt: "Review"}
			_, err := m.startSessionRun(ctx, pluginID, plugins.Config{Project: root}, params, root, "reviewer")
			if tc.foreign || tc.otherDirectory || tc.nested || tc.unsettled || tc.otherPlatform {
				if err == nil || plugins.RequestError(err).Category != plugins.ErrorPermissionDenied || creator.created != 0 {
					t.Fatalf("unauthorized child creation: created=%d err=%v", creator.created, err)
				}
				return
			}
			if err != nil || creator.created != 1 || creator.parentID != parent || len(creator.rules) != 1 || creator.rules[0].Action != "deny" {
				t.Fatalf("child creation lost parent or restricted policy: %+v err=%v", creator, err)
			}
		})
	}
}
