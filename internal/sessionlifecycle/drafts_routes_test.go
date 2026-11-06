package sessionlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDraftRoutesRequirePersistence(t *testing.T) {
	m := NewModule(Deps{})
	for _, call := range []struct{ method, path string }{
		{"GET", "/draft"}, {"PUT", "/draft"}, {"DELETE", "/draft"}, {"POST", "/draft/start"}, {"POST", "/draft/messages"}, {"POST", "/draft/retry-delivery"},
	} {
		if w := draftCall(m, call.method, call.path, `{}`); w.Code != 503 {
			t.Fatal(call, w.Code)
		}
	}
	w := httptest.NewRecorder()
	m.listDrafts(w, httptest.NewRequest(http.MethodGet, "/drafts", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestRecoveryRecognizesPersistedFirstDeliveryWithoutReplay(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.accept(t.Context(), draftID, []byte(`{"send":{"message":"first"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.confirmed(t.Context(), draftID, map[string]any{"sessionId": draftID, "firstMessageSent": false}); err != nil {
		t.Fatal(err)
	}
	if err := store.progress(t.Context(), draftID, "prompt", "done"); err != nil {
		t.Fatal(err)
	}
	if err := store.recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := store.get(t.Context(), draftID)
	if err != nil || row.Phase != "ready" || row.Error != "" || !bytes.Contains(row.Result, []byte(`"firstMessageSent":true`)) {
		t.Fatal(row, err)
	}
}

func TestDraftWorkerPanicKeepsDurableRecoveryReceipt(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.accept(t.Context(), draftID, []byte(`{"send":{"message":"saved"}}`)); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{DB: db.SQL()})
	m.runDraftWorker(t.Context(), draftID, func() { panic("test panic") })
	row, err := store.get(t.Context(), draftID)
	if err != nil || row.Phase != "failed" || row.Error == "" || !bytes.Contains(row.Request, []byte("saved")) {
		t.Fatal(row, err)
	}
}

func TestDraftValidationAndListProjection(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{DB: db.SQL()})
	for _, body := range []string{`{`, `[]`, `{}`, `{"directory":"relative"}`} {
		if w := draftCall(m, "PUT", "/draft", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if w := draftCall(m, "GET", "/draft", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := draftCall(m, "POST", "/draft/messages", `{"message":"hi"}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := draftCall(m, "PUT", "/draft", `{"directory":"/repo","text":"unsent"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := draftCall(m, "POST", "/draft/messages", `{"message":"hi"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := draftCall(m, "POST", "/draft/start", `{"directory":"/repo"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := draftCall(m, "POST", "/draft/retry-delivery", ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	store := draftStore{db.SQL()}
	if _, _, err := store.accept(t.Context(), draftID, []byte(`{"send":{"message":"first"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.finish(t.Context(), draftID, []byte(`{"sessionId":"`+draftID+`"}`), ""); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	m.listDrafts(w, httptest.NewRequest(http.MethodGet, "/drafts", nil))
	var rows []ConversationDraft
	if json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].Phase != "ready" || len(rows[0].Request) != 0 || len(rows[0].Result) != 0 {
		t.Fatal(w.Body.String())
	}
	w = draftCall(m, "GET", "/draft", "")
	var detail ConversationDraft
	if json.Unmarshal(w.Body.Bytes(), &detail) != nil || len(detail.Request) == 0 || len(detail.Result) == 0 {
		t.Fatal(w.Body.String())
	}
}

func TestExplicitOwnerDisconnectCannotAcceptOrQueueDraft(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{DB: db.SQL(), Owner: func(w http.ResponseWriter, _, owner string) (hostsvc.Host, bool) {
		if owner != "box" {
			t.Errorf("lost owner: %q", owner)
		}
		http.Error(w, "owner disconnected", http.StatusServiceUnavailable)
		return nil, false
	}})
	if w := draftCall(m, "PUT", "/draft", `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	request := `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode","send":{"message":"first"}}`
	if w := draftCall(m, "POST", "/draft/start", request); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	store := draftStore{db.SQL()}
	row, err := store.get(t.Context(), draftID)
	if err != nil || row.Phase != "draft" {
		t.Fatal(row, err)
	}
	if _, _, err := store.accept(t.Context(), draftID, []byte(request)); err != nil {
		t.Fatal(err)
	}
	if w := draftCall(m, "POST", "/draft/messages", `{"message":"next"}`); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestExplicitFirstDeliveryRetryNeverRecreatesSession(t *testing.T) {
	db := statetest.Open(t, Schema)
	var sends int
	m := NewModule(Deps{DB: db.SQL(), Sessions: func() Sessions { return &startSessions{} },
		Owner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return &startHost{}, true },
		CreatePlatform: func(_ context.Context, _ hostsvc.Host, id, model string) (string, string, error) {
			return id, model, nil
		},
		Broadcast: func(string, []byte) {}, SendNow: func(_ context.Context, _ string, req platforms.SendMessageRequest) error {
			sends++
			if req.SessionID != draftID || req.Message != "saved first" {
				return errors.New("payload changed")
			}
			return nil
		},
	})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer m.Finalize()
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
		t.Fatal(err)
	}
	request := `{"directory":"/repo","platform":"r-box:claudecode","remoteId":"box","send":{"message":"saved first"}}`
	if _, _, err := store.accept(t.Context(), draftID, []byte(request)); err != nil {
		t.Fatal(err)
	}
	if err := store.finish(t.Context(), draftID, []byte(`{"sessionId":"`+draftID+`","platform":"r-box:claudecode","directory":"/repo","firstMessageSent":false}`), "failed delivery"); err != nil {
		t.Fatal(err)
	}
	if w := draftCall(m, "POST", "/draft/retry-delivery", ""); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	m.Finalize()
	row, err := store.get(t.Context(), draftID)
	if err != nil || row.Phase != "ready" || sends != 1 || m.QueueBlocked(t.Context(), draftID) {
		t.Fatal(row, sends, err)
	}
}

type draftAttachmentHost struct {
	hostsvc.Host
	got hostsvc.ComposerAttachmentRequest
}

func (h *draftAttachmentHost) SaveComposerAttachment(_ context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	h.got = req
	if _, err := io.ReadAll(reader); err != nil {
		return nil, err
	}
	return &hostsvc.ComposerAttachment{Path: "/owner/cache/file.txt", Name: req.Name}, nil
}

func TestDraftAttachmentPersistsOnExplicitOwnerBeforeAllocation(t *testing.T) {
	db := statetest.Open(t, Schema)
	host := &draftAttachmentHost{}
	m := NewModule(Deps{DB: db.SQL(), Owner: func(_ http.ResponseWriter, dir, owner string) (hostsvc.Host, bool) {
		if dir != "/repo" || owner != "box" {
			t.Errorf("owner mismatch %q %q", dir, owner)
		}
		return host, true
	}, UploadReadTimeout: func() time.Duration { return time.Second }})
	if w := draftCall(m, "PUT", "/draft", `{"directory":"/repo","remoteId":"box"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("notes"))
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/draft/attachment", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.SetPathValue("id", draftID)
	w := httptest.NewRecorder()
	m.draftAttachment(w, r)
	if w.Code != 200 || host.got.SessionID != draftID || host.got.Name != "note.txt" {
		t.Fatal(w.Code, w.Body.String(), host.got)
	}
}
