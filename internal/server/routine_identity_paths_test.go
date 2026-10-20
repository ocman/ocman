package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/google/uuid"
)

func TestRoutineTrailingSlashRequestsPersistResolvedSessionOwner(t *testing.T) {
	for _, owner := range []string{"local", "remote"} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(owner+" "+method, func(t *testing.T) {
				s, _, ownedID := identityHTTPFixture(t)
				platform := "test"
				if owner == "remote" {
					ownedID, platform = "ocm_"+uuid.NewString(), "r-remote:test"
					s.registry.Register(&taskIdentityPlatform{fakePlatform: &fakePlatform{id: platform}, info: platforms.SessionIdentity{ID: ownedID, OwnerID: "remote", Platform: "test", SourceKey: "remote-source", NativeID: "native", Origin: "external", CreationState: "ready"}})
				}
				mux, err := s.routes()
				if err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(map[string]any{"name": "Existing session", "prompt": "inspect", "directory": "/repo", "platform": platform, "remoteId": owner, "sessionMode": "existing", "sessionId": ownedID, "schedule": map[string]string{"kind": "none"}})
				if err != nil {
					t.Fatal(err)
				}
				path, code := "/api/routines/", http.StatusCreated
				if method == http.MethodPut {
					created := doRoutineRequest(t, mux, http.MethodPost, "/api/routines", string(body))
					var item state.Routine
					if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil || created.Code != http.StatusCreated {
						t.Fatalf("setup create: %d %s %v", created.Code, created.Body.String(), err)
					}
					path, code = "/api/routines/"+item.ID+"/", http.StatusOK
				}
				response := doRoutineRequest(t, mux, method, path, string(body))
				var item state.Routine
				if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || response.Code != code {
					t.Fatalf("routine request: %d %s %v", response.Code, response.Body.String(), err)
				}
				stored, err := s.stateDB.GetRoutine(t.Context(), item.ID)
				if err != nil || stored.SessionID != "native" || stored.Platform != platform || stored.RemoteID != owner {
					t.Fatalf("accepted route persisted unresolved identity: %+v %v", stored, err)
				}
				if item.SessionID != ownedID {
					t.Fatalf("response lost canonical session ID: %+v", item)
				}
			})
		}
	}
}
