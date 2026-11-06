package sessionlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDraftStorageFailuresStayVisibleAndBlockQueue(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{DB: db.SQL()})
	if err := db.SQL().Close(); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if w := draftCall(m, method, "/draft", `{"directory":"/repo"}`); w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	m.listDrafts(w, httptest.NewRequest(http.MethodGet, "/drafts", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code, w.Body.String())
	}
	if !m.QueueBlocked(t.Context(), draftID) {
		t.Fatal("storage failure released queue")
	}
	if err := m.Start(t.Context()); err == nil {
		t.Fatal("storage failure hidden at startup")
	}
}

func TestDraftAdmissionMissingConflictAndStoppedWorker(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{DB: db.SQL(), Sessions: func() Sessions { return &startSessions{} },
		Owner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return &startHost{}, true },
		CreatePlatform: func(_ context.Context, _ hostsvc.Host, id, model string) (string, string, error) {
			return id, model, nil
		},
	})
	payload := `{"directory":"/repo","platform":"r-box:claudecode","send":{"message":"saved"}}`
	if w := draftCall(m, http.MethodPost, "/draft/start", payload); w.Code != http.StatusNotFound {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := draftCall(m, http.MethodPost, "/draft/start", `{`); w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
	if w := draftCall(m, http.MethodPost, "/draft/messages", `{`); w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
	if w := draftCall(m, http.MethodPut, "/draft", `{"directory":"/repo"}`); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	m.Finalize()
	if w := draftCall(m, http.MethodPost, "/draft/start", payload); w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	store := draftStore{db.SQL()}
	row, err := store.get(t.Context(), draftID)
	if err != nil || row.Phase != "failed" || row.Error == "" {
		t.Fatal(row, err)
	}
	if w := draftCall(m, http.MethodPost, "/draft/start", `{"directory":"/repo","platform":"r-box:claudecode","send":{"message":"different"}}`); w.Code != http.StatusConflict {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := db.SQL().Close(); err != nil {
		t.Fatal(err)
	}
	if w := draftCall(m, http.MethodPost, "/draft/start", payload); w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDraftQueueReportsUnavailableAndFailedAdmission(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.accept(t.Context(), draftID, []byte(`{"directory":"/repo","platform":"opencode","send":{"message":"first"}}`)); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{DB: db.SQL(), Owner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return &startHost{}, true }})
	if w := draftCall(m, http.MethodPost, "/draft/messages", `{"message":"next"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Code, w.Body.String())
	}
	m.d.Enqueue = func(context.Context, string, platforms.SendMessageRequest) error {
		return errors.New("queue disk full")
	}
	if w := draftCall(m, http.MethodPost, "/draft/messages", `{"message":"next"}`); w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code, w.Body.String())
	}
}
