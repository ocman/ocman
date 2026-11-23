package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDraftSaveOverEmptyServerText(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, revision string
	}{
		{"new editor", `{"directory":"/repo","text":""}`, ""},
		{"stale editor", `{"directory":"/repo","text":""}`, "stale"},
		{"omitted text", `{"directory":"/repo"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			m := NewModule(Deps{DB: db.SQL()})
			if w := draftCall(m, http.MethodPut, "/draft", tc.metadata); w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			const edit = `{"directory":"/repo","text":"first prompt"}`
			r := httptest.NewRequest(http.MethodPut, "/draft", strings.NewReader(edit))
			r.SetPathValue("id", draftID)
			r.Header.Set("If-Match", tc.revision)
			w := httptest.NewRecorder()
			m.saveDraft(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("saving over an empty draft: got %d, want 200: %s", w.Code, w.Body.String())
			}
			row, err := (draftStore{db.SQL()}).get(t.Context(), draftID)
			if err != nil || string(row.Metadata) != edit {
				t.Fatalf("edit not persisted: %s (%v)", row.Metadata, err)
			}
		})
	}
}

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

func TestDraftSubmitOnlyOverridesOldEdits(t *testing.T) {
	for _, tc := range []struct {
		name, intent string
		age          time.Duration
		status       int
	}{
		{"send over old draft", "submit", 6 * time.Minute, http.StatusOK},
		{"send over recent edits to an old draft", "submit", time.Minute, http.StatusConflict},
		{"autosave over old draft", "", 6 * time.Minute, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			m := NewModule(Deps{DB: db.SQL()})
			const peer = `{"directory":"/repo","text":"peer edits"}`
			const local = `{"directory":"/repo","text":"my prompt"}`
			if w := draftCall(m, http.MethodPut, "/draft", peer); w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			now := time.Now()
			if _, err := db.SQL().Exec(`UPDATE conversation_draft SET created_at=?,updated_at=? WHERE id=?`, now.Add(-time.Hour).UnixMilli(), now.Add(-tc.age).UnixMilli(), draftID); err != nil {
				t.Fatal(err)
			}
			put := func(body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPut, "/draft", strings.NewReader(body))
				r.SetPathValue("id", draftID)
				r.Header.Set("If-Match", "stale")
				r.Header.Set("X-Ocman-Draft-Intent", tc.intent)
				w := httptest.NewRecorder()
				m.saveDraft(w, r)
				return w
			}
			if w := put(local); w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			want := peer
			if tc.status == http.StatusOK {
				want = local
				if w := put(peer); w.Code != http.StatusConflict {
					t.Fatalf("a second stale send must not overwrite the fresh save: %d %s", w.Code, w.Body.String())
				}
			}
			var draft ConversationDraft
			w := draftCall(m, http.MethodGet, "/draft", "")
			if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil || string(draft.Metadata) != want {
				t.Fatalf("got %s, want metadata %s (%v)", w.Body.String(), want, err)
			}
		})
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
	if _, err := store.saveVersion(ctx, draftID, json.RawMessage(`{}`), "", false); err == nil {
		t.Fatal("canceled save succeeded")
	}
	if _, err := store.saveVersion(t.Context(), draftID, json.RawMessage(`{}`), "missing", false); err == nil {
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
			if _, err := (draftStore{db.SQL()}).saveVersion(t.Context(), draftID, json.RawMessage(`{}`), "", false); err == nil {
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
