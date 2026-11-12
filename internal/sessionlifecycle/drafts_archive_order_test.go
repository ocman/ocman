package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestArchiveAcknowledgesExistingFailureButNotLaterIdenticalFailure(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.accept(t.Context(), draftID, []byte(`{"platform":"opencode","send":{"message":"first"}}`)); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{DB: db.SQL(), ArchiveWatermark: func(context.Context, string, string) (int64, error) { return 100, nil },
		Archive: func(ctx context.Context, platform, id string, updated int64, _ bool) error {
			return db.ArchiveSession(ctx, platform, id, updated)
		}})
	if err := m.finishDraft(t.Context(), draftID, []byte(`{"sessionId":"`+draftID+`"}`), "failed delivery"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/archive", nil))
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	row, err := store.get(t.Context(), draftID)
	if err != nil || !row.Archived || !row.ErrorAcknowledged || row.ErrorVersion != 1 {
		t.Fatal(row, err)
	}
	if err := m.finishDraft(t.Context(), draftID, nil, "failed delivery"); err != nil {
		t.Fatal(err)
	}
	row, err = store.get(t.Context(), draftID)
	if err != nil || row.ErrorAcknowledged || row.ErrorVersion != 2 {
		t.Fatal(row, err)
	}
	// Reloading the module cannot forget which error the archive acknowledged.
	if err := NewModule(Deps{DB: db.SQL()}).Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err = store.get(t.Context(), draftID)
	if err != nil || row.ErrorAcknowledged {
		t.Fatal(row, err)
	}
}

func TestArchiveOrdersCompletionBeforeOrAfterItsWatermark(t *testing.T) {
	for _, completeFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "archive first", true: "completion first"}[completeFirst], func(t *testing.T) {
			db := statetest.Open(t, Schema)
			store := draftStore{db.SQL()}
			if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.accept(t.Context(), draftID, []byte(`{"platform":"r-box:opencode","send":{"message":"first"}}`)); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			watermark := int64(0)
			m := NewModule(Deps{DB: db.SQL(), ArchiveWatermark: func(context.Context, string, string) (int64, error) { return 101, nil },
				Archive: func(_ context.Context, _, _ string, updated int64, _ bool) error {
					watermark = updated
					close(entered)
					<-release
					return nil
				}})
			if completeFirst {
				if err := m.finishDraft(t.Context(), draftID, nil, ""); err != nil {
					t.Fatal(err)
				}
			}
			archiveDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/archive", nil))
				archiveDone <- w
			}()
			<-entered
			finishDone := make(chan error, 1)
			if !completeFirst {
				go func() { finishDone <- m.finishDraft(t.Context(), draftID, nil, "") }()
			}
			// The archive owns the ordering lock while it commits its marker.
			row, err := store.get(t.Context(), draftID)
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			if !completeFirst && row.Phase != "accepted" {
				close(release)
				t.Fatal(row.Phase)
			}
			close(release)
			if w := <-archiveDone; w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			if !completeFirst {
				if err := <-finishDone; err != nil {
					t.Fatal(err)
				}
			}
			want := int64(1)
			if completeFirst {
				want = 101
			}
			if watermark != want {
				t.Fatalf("watermark=%d want=%d", watermark, want)
			}
		})
	}
}
