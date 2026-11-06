package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDraftReversedArrivalRejectsStaleVersion(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{DB: db.SQL()})
	w := draftCall(m, "PUT", "/draft", `{"directory":"/repo","text":"original"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var original ConversationDraft
	if err := json.Unmarshal(w.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	put := func(text string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/draft", strings.NewReader(`{"directory":"/repo","text":"`+text+`"}`))
		r.SetPathValue("id", draftID)
		r.Header.Set("If-Match", original.Revision)
		response := httptest.NewRecorder()
		m.saveDraft(response, r)
		return response
	}
	if w := put("newer"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := put("older delayed"); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := put("newer"); w.Code != 200 {
		t.Fatal("idempotent save replay", w.Code)
	}
	row, err := (draftStore{db.SQL()}).get(t.Context(), draftID)
	if err != nil || !strings.Contains(string(row.Metadata), "newer") {
		t.Fatal(row, err)
	}
}

func TestDraftVersionStorageFailureAndCanceledAdmission(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := store.accept(ctx, draftID, []byte(`{}`)); err == nil {
		t.Fatal("canceled admission succeeded")
	}
	if _, err := store.saveVersion(ctx, draftID, json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("canceled save succeeded")
	}
	if _, err := store.saveVersion(t.Context(), draftID, json.RawMessage(`{}`), "missing"); err == nil {
		t.Fatal("missing version succeeded")
	}
	if err := db.SQL().Close(); err != nil {
		t.Fatal(err)
	}
	if w := draftCall(NewModule(Deps{DB: db.SQL()}), http.MethodPut, "/draft", `{"directory":"/repo"}`); w.Code != 500 {
		t.Fatal(w.Code)
	}
}

func TestDraftVersionRollsBackFailedWritesAndReceipts(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER fail_save BEFORE INSERT ON conversation_draft BEGIN SELECT RAISE(ABORT,'disk failure'); END`,
		`CREATE TRIGGER fail_save AFTER INSERT ON conversation_draft BEGIN UPDATE conversation_draft SET steps='invalid'; END`,
	} {
		t.Run(trigger, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			if _, err := db.SQL().Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := (draftStore{db.SQL()}).saveVersion(t.Context(), draftID, json.RawMessage(`{}`), ""); err == nil {
				t.Fatal("save failure hidden")
			}
			var count int
			if err := db.SQL().QueryRow(`SELECT count(*) FROM conversation_draft`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestDraftAdmissionReceiptFailureDoesNotCommit(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE conversation_draft SET steps='invalid' WHERE id=?`, draftID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.accept(t.Context(), draftID, []byte(`{"send":{"message":"first"}}`)); err == nil {
		t.Fatal("expected receipt failure")
	}
	var phase string
	if err := db.SQL().QueryRow(`SELECT phase FROM conversation_draft WHERE id=?`, draftID).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if phase != "draft" {
		t.Fatalf("admission committed without receipt: %s", phase)
	}
}

func TestDraftAttachmentNormalizesOwnerWithoutRuntime(t *testing.T) {
	for _, tc := range []struct {
		meta       string
		status     int
		ownerCalls int
	}{
		{`{"directory":"/repo","platform":"r-box:claudecode"}`, 503, 1},
		{`{"directory":"/repo","platform":"r-box:claudecode","remoteId":"local"}`, 400, 0},
	} {
		t.Run(tc.meta, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			calls := 0
			m := NewModule(Deps{DB: db.SQL(), Owner: func(w http.ResponseWriter, _, owner string) (hostsvc.Host, bool) {
				calls++
				if owner != "box" {
					t.Errorf("owner = %q", owner)
				}
				http.Error(w, "disconnected", http.StatusServiceUnavailable)
				return nil, false
			}})
			if err := (draftStore{db.SQL()}).save(t.Context(), draftID, json.RawMessage(tc.meta)); err != nil {
				t.Fatal(err)
			}
			w := draftCall(m, "POST", "/draft/attachment", "")
			if w.Code != tc.status || calls != tc.ownerCalls {
				t.Fatalf("status=%d calls=%d", w.Code, calls)
			}
		})
	}
}
