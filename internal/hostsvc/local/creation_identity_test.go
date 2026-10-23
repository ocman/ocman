package local

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

func TestWorktreeCreationCarriesIdentityAndPreservesUncertainExecution(t *testing.T) {
	for _, uncertain := range []bool{true, false} {
		t.Run(map[bool]string{true: "uncertain", false: "rejected"}[uncertain], func(t *testing.T) {
			repo := initRepo(t)
			var directory string
			creation := &platforms.CreationIdentity{RequestKey: "run", Origin: "routine", OriginID: "rt"}
			h := New(Deps{Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"},
				CreatePlatformSession: func(_ context.Context, _ string, req platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
					directory = req.Directory
					if !reflect.DeepEqual(req.Creation, creation) {
						t.Fatalf("creation intent lost: %+v", req.Creation)
					}
					cause := errors.New("creation failed")
					if uncertain {
						return nil, &platforms.CreationPendingError{SessionID: "ocm_owned", Err: cause}
					}
					return nil, cause
				}})
			_, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{ProjectDir: repo, Branch: "identity", NewBranch: true, MustCreateBranch: true, BaseRef: "main", Creation: creation})
			if err == nil || directory == "" {
				t.Fatalf("creation did not run: %v", err)
			}
			_, statErr := os.Stat(directory)
			if uncertain && statErr != nil {
				t.Fatalf("uncertain execution workspace removed: %v", statErr)
			}
			if !uncertain && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("definite rejection workspace not rolled back: %v", statErr)
			}
		})
	}
}

func TestWorktreeRetainsWorkspaceAfterFailedNativeDisposal(t *testing.T) {
	repo := initRepo(t)
	var directory string
	h := New(Deps{Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"},
		CreatePlatformSession: func(_ context.Context, _ string, req platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
			directory = req.Directory
			return nil, &sessionsvc.ConfiguredSessionCleanupError{SessionID: "still-live", Err: errors.New("disposal failed")}
		}})
	_, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{ProjectDir: repo, Branch: "cleanup", NewBranch: true, MustCreateBranch: true, BaseRef: "main"})
	if err == nil || directory == "" {
		t.Fatalf("creation did not run: %v", err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("workspace removed while native execution may still exist: %v", err)
	}
}

func TestWorktreeRetainsWorkspaceForCanonicalCreationReconciliation(t *testing.T) {
	repo := initRepo(t)
	var directory string
	h := New(Deps{Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"}, CreatePlatformSession: func(_ context.Context, _ string, req platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
		directory = req.Directory
		return nil, &platforms.CreationPendingError{SessionID: "ocm_canonical", Err: errors.New("binding commit uncertain")}
	}})
	_, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{ProjectDir: repo, Branch: "canonical-recovery", NewBranch: true, MustCreateBranch: true, BaseRef: "main"})
	if err == nil || directory == "" {
		t.Fatalf("creation did not run: %v", err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("canonical recovery workspace removed: %v", err)
	}
}
