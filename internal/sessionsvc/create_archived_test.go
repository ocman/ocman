package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestCreateArchivesOnlyWhenRequested(t *testing.T) {
	for _, tc := range []struct {
		name     string
		creation *platforms.CreationIdentity
		want     bool
	}{
		{name: "not requested", creation: nil},
		{name: "requested", creation: &platforms.CreationIdentity{RequestKey: "k", Origin: "user", Archived: true}, want: true},
		{name: "plugin origin alone does not archive", creation: &platforms.CreationIdentity{RequestKey: "k", Origin: "plugin", OriginID: "org.example.background"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePlatform{id: "opencode", available: true, owned: map[string]bool{"new-session": true}}
			var archived []string
			var published []CreatedSession
			svc, _ := newService(p, Hooks{
				ArchiveCreated: func(_ context.Context, platform, id string) error {
					if len(published) != 0 {
						t.Error("archive ran after the row was published")
					}
					archived = append(archived, platform+"/"+id)
					return nil
				},
				SessionCreated: func(info CreatedSession) { published = append(published, info) },
			})
			resp, err := svc.Create(t.Context(), "", platforms.CreateSessionRequest{Directory: "/tmp", Creation: tc.creation})
			if err != nil || resp.ID != "new-session" {
				t.Fatalf("Create = %v, %v", resp, err)
			}
			if tc.want != (len(archived) == 1) || len(published) != 1 || published[0].Archived != tc.want {
				t.Fatalf("archive calls = %v, published = %+v, want archived %v", archived, published, tc.want)
			}
			if tc.creation != nil && (published[0].Origin != tc.creation.Origin || published[0].OriginID != tc.creation.OriginID) {
				t.Fatalf("published origin = %q/%q, want %q/%q", published[0].Origin, published[0].OriginID, tc.creation.Origin, tc.creation.OriginID)
			}
		})
	}
}

func TestCreateArchiveFailureDisposesWithoutPublishing(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true, owned: map[string]bool{"new-session": true}}
	published := 0
	svc, _ := newService(p, Hooks{
		ArchiveCreated: func(context.Context, string, string) error { return errors.New("archive failed") },
		SessionCreated: func(CreatedSession) { published++ },
	})
	_, err := svc.Create(t.Context(), "", platforms.CreateSessionRequest{Directory: "/tmp",
		Creation: &platforms.CreationIdentity{RequestKey: "k", Origin: "user", Archived: true}})
	if err == nil || published != 0 || p.owned["new-session"] {
		t.Fatalf("Create error = %v, published = %d, session remains = %v", err, published, p.owned["new-session"])
	}
}

func TestCreateArchivedWithoutHookIsUnsupported(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc, _ := newService(p, Hooks{})
	_, err := svc.Create(t.Context(), "", platforms.CreateSessionRequest{Directory: "/tmp",
		Creation: &platforms.CreationIdentity{RequestKey: "k", Origin: "user", Archived: true}})
	if !errors.Is(err, platforms.ErrUnsupported) || len(p.creates) != 0 {
		t.Fatalf("Create = %v, creates = %d", err, len(p.creates))
	}
}
