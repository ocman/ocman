package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestCreateArchivesOnlyWhenRequested(t *testing.T) {
	for _, tc := range []struct {
		name       string
		creation   *platforms.CreationIdentity
		archiveErr error
		want       bool
	}{
		{name: "not requested", creation: nil},
		{name: "requested", creation: &platforms.CreationIdentity{RequestKey: "k", Origin: "user", Archived: true}, want: true},
		{name: "plugin origin alone does not archive", creation: &platforms.CreationIdentity{RequestKey: "k", Origin: "plugin", OriginID: "org.example.background"}},
		{name: "archive failure keeps session", creation: &platforms.CreationIdentity{RequestKey: "k", Origin: "user", Archived: true}, archiveErr: errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePlatform{id: "opencode", available: true}
			var archived []string
			var published []CreatedSession
			svc, _ := newService(p, Hooks{
				ArchiveCreated: func(_ context.Context, platform, id string) error {
					if len(published) != 0 {
						t.Error("archive ran after the row was published")
					}
					archived = append(archived, platform+"/"+id)
					return tc.archiveErr
				},
				SessionCreated: func(info CreatedSession) { published = append(published, info) },
			})
			resp, err := svc.Create(t.Context(), "", platforms.CreateSessionRequest{Directory: "/tmp", Creation: tc.creation})
			if err != nil || resp.ID != "new-session" {
				t.Fatalf("Create = %v, %v", resp, err)
			}
			requested := tc.creation != nil && tc.creation.Archived
			if requested != (len(archived) == 1) {
				t.Fatalf("archive calls = %v, requested %v", archived, requested)
			}
			if len(published) != 1 || published[0].Archived != tc.want {
				t.Fatalf("published = %+v, want archived %v", published, tc.want)
			}
		})
	}
}
