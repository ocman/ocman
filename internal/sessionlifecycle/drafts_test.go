package sessionlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

const draftID = "ocm_01234567-89ab-4cde-8fab-0123456789ab"

func draftCall(m *Module, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetPathValue("id", draftID)
	w := httptest.NewRecorder()
	switch {
	case strings.HasSuffix(path, "/attachment"):
		m.draftAttachment(w, r)
	case strings.HasSuffix(path, "/retry-delivery"):
		m.retryDraftDelivery(w, r)
	case strings.HasSuffix(path, "/start"):
		m.acceptDraft(w, r)
	case strings.HasSuffix(path, "/messages"):
		m.enqueueDraft(w, r)
	case method == http.MethodPut:
		m.saveDraft(w, r)
	case method == http.MethodDelete:
		m.deleteDraft(w, r)
	default:
		m.getDraft(w, r)
	}
	return w
}

func TestDraftPayloadCommitsBeforeSetupAndKeepsIdentity(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "first delivery failure"}[fail], func(t *testing.T) {
			db := statetest.Open(t, Schema)
			var calls atomic.Int32
			release := make(chan struct{})
			var steps []string
			host := &startHost{steps: &steps}
			var queued platforms.SendMessageRequest
			svc := &startSessions{create: func(req platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
				calls.Add(1)
				<-release
				if req.Creation == nil || req.Creation.SessionID != draftID || req.Creation.RequestKey != "draft:"+draftID {
					return nil, errors.New("draft identity was not passed to native allocation")
				}
				return &platforms.CreateSessionResponse{ID: req.Creation.SessionID}, nil
			}}
			var sends atomic.Int32
			m := NewModule(Deps{DB: db.SQL(), Sessions: func() Sessions { return svc },
				Archive: func(ctx context.Context, platform, id string, updated int64, _ bool) error {
					return db.ArchiveSession(ctx, platform, id, updated)
				},
				Owner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return host, true },
				CreatePlatform: func(_ context.Context, _ hostsvc.Host, id, model string) (string, string, error) {
					return id, model, nil
				},
				PermissionDefaults: func(context.Context, string, string) ([]platforms.PermissionRule, error) { return nil, nil },
				Broadcast:          func(string, []byte) {},
				SendNow: func(_ context.Context, platform string, req platforms.SendMessageRequest) error {
					sends.Add(1)
					if platform != "r-box:claudecode" || req.SessionID != draftID || req.Message != "first" {
						return errors.New("lost first payload")
					}
					if fail {
						return errors.New("upstream rejected first prompt")
					}
					return nil
				},
				Enqueue: func(_ context.Context, platform string, req platforms.SendMessageRequest) error {
					if platform != "r-box:claudecode" {
						return errors.New("wrong queue owner")
					}
					queued = req
					return nil
				},
			})
			if err := m.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			if w := draftCall(m, "PUT", "/api/session-drafts/"+draftID, `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode"}`); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			payload := `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode","prompt":"first","send":{"message":"first"}}`
			w := draftCall(m, "POST", "/api/session-drafts/"+draftID+"/start", payload)
			if w.Code != http.StatusAccepted {
				close(release)
				m.Finalize()
				t.Fatal(w.Code, w.Body.String())
			}
			var accepted ConversationDraft
			if json.Unmarshal(w.Body.Bytes(), &accepted) != nil || accepted.ID != draftID || !strings.Contains(string(accepted.Request), `"message":"first"`) {
				t.Fatal("acknowledged without persisting complete payload", w.Body.String())
			}
			if !m.QueueBlocked(t.Context(), draftID) {
				t.Fatal("setup did not hold follow-ups")
			}
			if w := draftCall(m, "POST", "/api/session-drafts/"+draftID+"/start", payload); w.Code != http.StatusAccepted {
				t.Fatal("duplicate acknowledgement", w.Code)
			}
			if w := draftCall(m, "POST", "/api/session-drafts/"+draftID+"/messages", `{"message":"next","model":"claudecode:acme/m"}`); w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			if queued.SessionID != draftID || queued.Message != "next" || queued.Model != "claudecode:acme/m" {
				t.Fatalf("follow-up not preserved: %+v", queued)
			}
			if w := draftCall(m, "DELETE", "/api/session-drafts/"+draftID, ""); w.Code != http.StatusConflict {
				t.Fatal("discarded submitted prompt")
			}
			archive := httptest.NewRecorder()
			moduletest.Mount(m).ServeHTTP(archive, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/archive", nil))
			if archive.Code != http.StatusNoContent {
				close(release)
				m.Finalize()
				t.Fatal(archive.Code, archive.Body.String())
			}
			close(release)
			m.Finalize()
			stored, err := (draftStore{db.SQL()}).get(t.Context(), draftID)
			if err != nil || !stored.Archived || calls.Load() != 1 || sends.Load() != 1 || (stored.Phase == "ready") == fail || m.QueueBlocked(t.Context(), draftID) != fail {
				t.Fatalf("final receipt: %+v creates=%d sends=%d err=%v", stored, calls.Load(), sends.Load(), err)
			}
			if !strings.Contains(string(stored.Result), draftID) {
				t.Fatal("native session replaced draft identity")
			}
		})
	}
}

func TestDraftRecoveryNeverReplaysUncertainSetup(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.accept(t.Context(), draftID, []byte(`{"send":{"message":"saved"}}`)); err != nil || !created {
		t.Fatal(created, err)
	}
	m := NewModule(Deps{DB: db.SQL()})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.Finalize()
	row, err := store.get(t.Context(), draftID)
	if err != nil || row.Phase != "interrupted" || !strings.Contains(string(row.Request), "saved") || !m.QueueBlocked(t.Context(), draftID) {
		t.Fatalf("lost interrupted prompt: %+v %v", row, err)
	}
}

func TestDiscardedDraftCannotBeResurrected(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{DB: db.SQL()})
	path := "/api/session-drafts/" + draftID
	meta := `{"directory":"/repo"}`
	if w := draftCall(m, "PUT", path, meta); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := draftCall(m, "DELETE", path, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := draftCall(m, "PUT", path, meta); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	row, err := (draftStore{db.SQL()}).get(t.Context(), draftID)
	if err != nil || row.Phase != "discarded" {
		t.Fatal(row, err)
	}
}
