package sessionlifecycle

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDiscardFailedSetupWithoutRemote(t *testing.T) {
	for _, phase := range []string{"failed", "interrupted"} {
		for _, result := range []string{"", `{"sessionId":"old-remote-identity"}`} {
			t.Run(phase+result, func(t *testing.T) {
				db := statetest.Open(t, Schema)
				_, err := db.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,request,steps,error,created_at) VALUES(?, '{"directory":"/repo","remoteId":"box"}', ?, '{"send":{"message":"saved"}}', '{"session":"error"}', 'owner did not preserve the draft identity', 1)`, draftID, phase)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.SQL().Exec(`UPDATE conversation_draft SET result=? WHERE id=?`, result, draftID); err != nil {
					t.Fatal(err)
				}
				var changes int
				m := NewModule(Deps{DB: db.SQL(), Broadcast: func(string, []byte) { changes++ }})
				for range 2 {
					w := draftCall(m, http.MethodDelete, "/draft", "")
					if w.Code != http.StatusNoContent {
						t.Fatalf("cannot clean up %s setup without remote: %d %s", phase, w.Code, w.Body.String())
					}
				}
				row, err := (draftStore{db.SQL()}).get(t.Context(), draftID)
				if err != nil || row.Phase != "discarded" || string(row.Metadata) != "{}" || len(row.Request) != 0 || row.Error != "" || len(row.Steps) != 0 || changes != 2 {
					t.Fatalf("discard retained payload or missed notification: %+v changes=%d err=%v", row, changes, err)
				}
				if !m.QueueBlocked(t.Context(), draftID) {
					t.Fatal("discard released held messages")
				}
			})
		}
	}
}

func TestOutdatedOwnerDraftCanBeDiscardedWithoutSending(t *testing.T) {
	db := statetest.Open(t, Schema)
	svc := &startSessions{create: func(platforms.CreateSessionRequest, []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
		return &platforms.CreateSessionResponse{ID: "old-owner-session"}, nil
	}}
	var steps []string
	m := NewModule(Deps{DB: db.SQL(), Sessions: func() Sessions { return svc },
		Owner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return &startHost{steps: &steps}, true },
		CreatePlatform: func(_ context.Context, _ hostsvc.Host, id, model string) (string, string, error) {
			return id, model, nil
		},
		PermissionDefaults: func(context.Context, string, string) ([]platforms.PermissionRule, error) { return nil, nil },
		Broadcast:          func(string, []byte) {},
		SendNow: func(context.Context, string, platforms.SendMessageRequest) error {
			t.Error("sent first prompt to a mismatched session")
			return nil
		},
	})
	if w := draftCall(m, http.MethodPut, "/draft", `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode"}`); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := draftCall(m, http.MethodPost, "/draft/start", `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode","send":{"message":"saved"}}`); w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	m.Finalize()
	row, err := (draftStore{db.SQL()}).get(t.Context(), draftID)
	if err != nil || row.Phase != "failed" || !strings.Contains(row.Error, "Update ocman") {
		t.Fatalf("missing actionable outdated-owner failure: %+v err=%v", row, err)
	}
	if w := draftCall(m, http.MethodDelete, "/draft", ""); w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDiscardKeepsActiveAndConfirmedSessions(t *testing.T) {
	for _, phase := range []string{"accepted", "starting", "delivering", "ready", "failed", "interrupted"} {
		t.Run(phase, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			_, err := db.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,result,created_at) VALUES(?, '{}', ?, ?, 1)`, draftID, phase, `{"sessionId":"`+draftID+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			w := draftCall(NewModule(Deps{DB: db.SQL()}), http.MethodDelete, "/draft", "")
			if w.Code != http.StatusConflict {
				t.Fatalf("discarded %s bound session: %d", phase, w.Code)
			}
		})
	}
}
