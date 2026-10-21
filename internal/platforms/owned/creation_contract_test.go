package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type creationContractNative struct {
	*nativePlatform
	response        *platforms.CreateSessionResponse
	afterAllocation func()
}

func (p *creationContractNative) CreateSession(context.Context, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.creates++
	if p.afterAllocation != nil {
		p.afterAllocation()
	}
	return p.response, nil
}
func (*creationContractNative) SetPermissionRules(context.Context, platforms.SetPermissionRulesRequest) error {
	return platforms.ErrPlatformUnreachable
}

func TestUncertainNativeCreationOutcomesRemainUnpublishable(t *testing.T) {
	for _, kind := range []string{"nil response", "empty response", "source changed", "binding conflict"} {
		t.Run(kind, func(t *testing.T) {
			a, p, _ := fixture(t)
			native := &creationContractNative{nativePlatform: p, response: &platforms.CreateSessionResponse{ID: "allocated"}}
			req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "uncertain"}}
			switch kind {
			case "nil response":
				native.response = nil
			case "empty response":
				native.response = &platforms.CreateSessionResponse{}
			case "source changed":
				native.afterAllocation = func() { p.source = "changed" }
			case "binding conflict":
				if _, err := a.canonical(t.Context(), "native"); err != nil {
					t.Fatal(err)
				}
				native.response.ID = "native"
			}
			a.native = native
			response, err := a.CreateSession(t.Context(), req)
			var pending *CreationPendingError
			if response != nil || !errors.As(err, &pending) || !sessioncatalog.IsID(pending.SessionID) {
				t.Fatalf("uncertain allocation became replayable: %+v %v", response, err)
			}
			p.source = "source"
			if _, err := a.CreateSession(t.Context(), req); !errors.Is(err, sessioncatalog.ErrCreating) || native.creates != 1 {
				t.Fatalf("uncertain allocation replayed: creates=%d %v", native.creates, err)
			}
		})
	}
}

func TestForkRejectsConflictingConfigurationAndParentIntent(t *testing.T) {
	a, _, _ := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Configured: true}}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("undisposable configured creation allocated: %v", err)
	}
	for _, intent := range []*platforms.CreationIdentity{
		{Configured: true},
		{PermissionRules: []platforms.PermissionRule{{}}},
		{ParentID: "ocm_00000000-0000-4000-8000-000000000099"},
	} {
		if _, err := a.ForkSession(t.Context(), platforms.ForkSessionRequest{SessionID: row.ID, Creation: intent}); !errors.Is(err, sessioncatalog.ErrInvalid) {
			t.Fatalf("conflicting fork intent accepted: %v", err)
		}
	}
}
