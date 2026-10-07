package sessionsvc

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestCreateRoutineLinkFailureDisposesWithoutPublishing(t *testing.T) {
	for _, disposalFails := range []bool{false, true} {
		p := &fakePlatform{id: "opencode", available: true, owned: map[string]bool{"new-session": true}}
		if disposalFails {
			p.disposeErr = errors.New("dispose failed")
		}
		published := false
		svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{"opencode": p}}, Hooks{SessionCreated: func(CreatedSession) { published = true }})
		linkErr := errors.New("link failed")
		_, err := svc.CreateRoutine(t.Context(), "opencode", platforms.CreateSessionRequest{Directory: "/repo"}, nil, "rt", func(string) error { return linkErr })
		if !errors.Is(err, linkErr) || published || p.owned["new-session"] != disposalFails {
			t.Fatalf("error=%v, published=%v, session exists=%v", err, published, p.owned["new-session"])
		}
	}
}

func TestCreateRoutineInvalidRulesDoesNotCreateOrLink(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{"opencode": p}}, Hooks{})
	_, err := svc.CreateRoutine(t.Context(), "opencode", platforms.CreateSessionRequest{Directory: "/repo"}, []platforms.PermissionRule{{Action: "allow"}}, "rt", func(string) error { t.Fatal("linked invalid session"); return nil })
	if err == nil || len(p.creates) != 0 {
		t.Fatalf("error=%v, creates=%d", err, len(p.creates))
	}
}
