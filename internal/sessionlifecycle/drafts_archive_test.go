package sessionlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestSubmittedDraftCanBeArchivedWithoutDiscardingSetup(t *testing.T) {
	for _, phase := range []string{"accepted", "starting", "delivering"} {
		t.Run(phase, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			store := draftStore{db.SQL()}
			if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.accept(t.Context(), draftID, []byte(`{"platform":"r-box:opencode","send":{"message":"saved"}}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL().Exec(`UPDATE conversation_draft SET phase=? WHERE id=?`, phase, draftID); err != nil {
				t.Fatal(err)
			}
			m := NewModule(Deps{DB: db.SQL(), Archive: func(ctx context.Context, platform, id string, updated int64, archived bool) error {
				if platform != "r-box:opencode" || id != draftID || !archived || updated != 1 {
					t.Fatal(platform, id, archived, updated)
				}
				return db.ArchiveSession(ctx, platform, id, updated)
			}})
			w := httptest.NewRecorder()
			moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/archive", nil))
			if w.Code != http.StatusNoContent {
				t.Fatalf("cannot archive setup: %d %s", w.Code, w.Body.String())
			}
			row, err := store.get(context.Background(), draftID)
			if err != nil || !row.Archived || row.Phase != phase || string(row.Request) != `{"platform":"r-box:opencode","send":{"message":"saved"}}` {
				t.Fatalf("setup was changed: %+v %v", row, err)
			}
		})
	}
}

func TestSetupPending(t *testing.T) {
	if NewModule(Deps{}).SetupPending(t.Context(), draftID) {
		t.Fatal("nil persistence blocked a normal session")
	}
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{DB: db.SQL()})
	if m.SetupPending(t.Context(), "missing") {
		t.Fatal("native-only session blocked")
	}
	for _, phase := range []string{"draft", "accepted", "starting", "delivering", "ready", "failed", "interrupted", "discarded"} {
		if _, err := db.SQL().Exec(`INSERT OR REPLACE INTO conversation_draft(id,metadata,phase,created_at) VALUES(?,'{}',?,1)`, draftID, phase); err != nil {
			t.Fatal(err)
		}
		want := phase == "accepted" || phase == "starting" || phase == "delivering"
		if m.SetupPending(t.Context(), draftID) != want {
			t.Fatal(phase)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !m.SetupPending(ctx, draftID) {
		t.Fatal("failed setup read did not fail closed")
	}
}

func TestDraftArchiveGuards(t *testing.T) {
	for _, tc := range []struct {
		name, phase, request string
		available, fails     bool
		want                 int
	}{
		{"draft", "draft", "", true, false, 409},
		{"discarded", "discarded", "", true, false, 409},
		{"invalid request", "starting", "{", true, false, 409},
		{"missing platform", "starting", "{}", true, false, 409},
		{"unavailable", "starting", `{"platform":"opencode"}`, false, false, 503},
		{"write failure", "starting", `{"platform":"opencode"}`, true, true, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			if _, err := db.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,request,created_at) VALUES(?,'{}',?,?,1)`, draftID, tc.phase, tc.request); err != nil {
				t.Fatal(err)
			}
			d := Deps{DB: db.SQL()}
			if tc.available {
				d.Archive = func(context.Context, string, string, int64, bool) error {
					if tc.fails {
						return errors.New("disk full")
					}
					t.Fatal("unexpected archive")
					return nil
				}
			}
			w := httptest.NewRecorder()
			moduletest.Mount(NewModule(d)).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/archive", nil))
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestDraftArchivePersistenceAndMissingIdentity(t *testing.T) {
	for _, missingDB := range []bool{false, true} {
		d := Deps{}
		want := http.StatusServiceUnavailable
		if !missingDB {
			d.DB = statetest.Open(t, Schema).SQL()
			want = http.StatusNotFound
		}
		w := httptest.NewRecorder()
		moduletest.Mount(NewModule(d)).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/archive", nil))
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
