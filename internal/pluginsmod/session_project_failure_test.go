package pluginsmod

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessionsvc"
	"github.com/ocman/ocman/internal/state/statetest"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type associationFailureSessions struct {
	sourceTestSessions
	err error
}

func TestUncertainCreationKeepsProjectReservationFenced(t *testing.T) {
	for _, creationErr := range []error{
		&platforms.CreationPendingError{SessionID: "pending", Err: errors.New("native outcome unknown")},
		&sessionsvc.ConfiguredSessionCleanupError{SessionID: "created", Err: errors.New("disposal failed")},
		context.Canceled, context.DeadlineExceeded,
	} {
		t.Run(creationErr.Error(), func(t *testing.T) {
			root := t.TempDir()
			creator := &associationFailureSessions{err: creationErr}
			create, lookup := localPlatforms("opencode")
			database := statetest.Open(t, Schema)
			m := &Module{store: database, plugins: NewStore(database), deps: Deps{
				Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator, CreatePlatform: create, Platform: lookup,
			}}
			params := plugins.SessionRunParams{WorkspaceID: "workspace-a", AssociatedProjectID: "project-a", NoTools: true, Prompt: "Review"}
			config := plugins.Config{Project: root, AssociatedProject: "/projects/a"}
			if _, err := m.startSessionRun(t.Context(), "reader", config, params, root, "uncertain"); err == nil {
				t.Fatal("uncertain creation succeeded")
			}
			canonical, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.plugins.reserveSessionProject(t.Context(), "reader", "different", canonical, "/projects/b"); !errors.Is(err, plugins.ErrUnavailable) {
				t.Fatalf("uncertain native creation allowed reassignment: %v", err)
			}
		})
	}
}

func (s *associationFailureSessions) CreateConfigured(ctx context.Context, platform string, req platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.sourceTestSessions.CreateConfigured(ctx, platform, req, rules)
}

func TestFailedCreationDoesNotClaimProjectAssociation(t *testing.T) {
	for _, stage := range []string{"profile", "creation"} {
		t.Run(stage, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			creator := new(associationFailureSessions)
			create, lookup := localPlatforms("opencode")
			database := statetest.Open(t, Schema)
			m := &Module{store: database, plugins: NewStore(database), deps: Deps{
				Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator, CreatePlatform: create, Platform: lookup,
				Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error { return nil },
			}}
			params := plugins.SessionRunParams{WorkspaceID: "workspace-a", AssociatedProjectID: "wrong-project", NoTools: true, Prompt: "Review"}
			if stage == "profile" {
				params.PermissionProfile = "missing"
			} else {
				creator.err = errors.New("creation rejected")
			}
			config := plugins.Config{Project: root, AssociatedProject: "/projects/wrong"}
			if _, err := m.startSessionRun(t.Context(), "reader", config, params, root, "failed"); err == nil {
				t.Fatal("failed run unexpectedly succeeded")
			}
			associations, err := m.plugins.sessionProjectAssociations(t.Context())
			if err != nil || len(associations) != 0 {
				t.Fatalf("failed creation permanently claimed project: %+v %v", associations, err)
			}
			creator.err = nil
			params.PermissionProfile = ""
			params.AssociatedProjectID = "correct-project"
			config.AssociatedProject = "/projects/correct"
			if _, err := m.startSessionRun(t.Context(), "reader", config, params, root, "corrected"); err != nil {
				t.Fatalf("corrected operation could not reassign unused directory: %v", err)
			}
			associations, err = m.plugins.sessionProjectAssociations(t.Context())
			if err != nil || associations[root] != config.AssociatedProject || creator.created != 1 {
				t.Fatalf("successful creation lost association: %+v %v", associations, err)
			}
		})
	}
}

func TestPostCreationFailurePreservesProjectAssociation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	creator := new(associationFailureSessions)
	create, lookup := localPlatforms("opencode")
	database := statetest.Open(t, Schema)
	m := &Module{store: database, plugins: NewStore(database), deps: Deps{
		Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator, CreatePlatform: create, Platform: lookup,
		Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error {
			return errors.New("queue unavailable")
		},
	}}
	params := plugins.SessionRunParams{WorkspaceID: "workspace-a", AssociatedProjectID: "project-a", NoTools: true, Prompt: "Review"}
	config := plugins.Config{Project: root, AssociatedProject: "/projects/a"}
	if _, err := m.startSessionRun(t.Context(), "reader", config, params, root, "queue-failed"); err == nil || creator.created != 1 {
		t.Fatalf("expected failure after creation: created=%d err=%v", creator.created, err)
	}
	// A failed later creation sharing the committed association must not release it.
	creator.err = errors.New("creation rejected")
	if _, err := m.startSessionRun(t.Context(), "reader", config, params, root, "later-failed"); err == nil {
		t.Fatal("later failed creation succeeded")
	}
	associations, err := m.plugins.sessionProjectAssociations(t.Context())
	if err != nil || associations[root] != config.AssociatedProject {
		t.Fatalf("failed cleanup erased an existing session's project: %+v %v", associations, err)
	}
	config.AssociatedProject = "/projects/b"
	params.AssociatedProjectID = "project-b"
	if _, err := m.startSessionRun(t.Context(), "reader", config, params, root, "reassigned"); err == nil {
		t.Fatal("existing session's directory was reassigned")
	}
}
