package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

type retrySetupSessions struct{ Sessions }

func (*retrySetupSessions) KnownPlatform(string) bool { return true }

type retrySetupHost struct {
	hostsvc.Host
	create func(hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error)
}

func (h *retrySetupHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	return h.create(req)
}

func TestDraftSetupRetryPreservesPayloadAndRejectsDuplicates(t *testing.T) {
	db := statetest.Open(t, Schema)
	store := draftStore{db.SQL()}
	if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo","platform":"opencode"}`)); err != nil {
		t.Fatal(err)
	}
	payload := `{"directory":"/repo","platform":"opencode","remoteId":"local","worktree":true,"prompt":"first","draftId":"` + draftID + `","startId":"` + draftID + `","send":{"message":"first","model":"acme/m","agent":"plan","reasoning":"high","images":[{"url":"data:image/png;base64,a","mime":"image/png"}]},"permissionRules":[]}`
	if _, _, err := store.accept(t.Context(), draftID, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := store.progress(t.Context(), draftID, hostsvc.StepWorktree, hostsvc.StepError); err != nil {
		t.Fatal(err)
	}
	if err := store.finish(t.Context(), draftID, nil, "Session setup failed. checkout timed out"); err != nil {
		t.Fatal(err)
	}
	var creates, sends, drains atomic.Int32
	release := make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	host := &retrySetupHost{create: func(req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
		creates.Add(1)
		<-release
		if req.Creation == nil || req.Creation.SessionID != draftID || req.Creation.Model != "acme/m" || req.Creation.Agent != "plan" || req.Creation.Reasoning != "high" || req.PermissionRules == nil {
			t.Error("retry lost identity or initial selections", req)
		}
		return &hostsvc.WorktreeSessionResult{SessionID: draftID, WorktreePath: "/worktree", Branch: "retry"}, nil
	}}
	m := NewModule(Deps{DB: db.SQL(), Sessions: func() Sessions { return &retrySetupSessions{} },
		Broadcast: func(string, []byte) {},
		Owner:     func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return host, true },
		CreatePlatform: func(_ context.Context, _ hostsvc.Host, id, model string) (string, string, error) {
			return id, model, nil
		},
		SendNow: func(_ context.Context, _ string, req platforms.SendMessageRequest) error {
			sends.Add(1)
			if req.SessionID != draftID || req.Message != "first" || len(req.Images) != 1 {
				t.Error("retry lost first message", req)
			}
			return nil
		},
		DispatchQueued: func(context.Context, string, string) { drains.Add(1) },
	})
	// Bind workers without restart recovery changing this completed failure.
	m.workers.Bind(t.Context())
	t.Cleanup(func() { unblock(); m.Finalize() })
	router := moduletest.Mount(m)
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/retry-setup", nil))
		return w
	}
	if w := call(); w.Code != http.StatusAccepted {
		t.Fatalf("failed checkout has no usable retry: %d %s", w.Code, w.Body.String())
	}
	if !m.QueueBlocked(t.Context(), draftID) {
		t.Fatal("retry released follow-ups before first delivery")
	}
	if w := call(); w.Code != http.StatusConflict {
		t.Fatalf("duplicate retry accepted: %d %s", w.Code, w.Body.String())
	}
	unblock()
	m.Finalize()
	row, err := store.get(t.Context(), draftID)
	if err != nil || row.Phase != "ready" || string(row.Request) != payload || creates.Load() != 1 || sends.Load() != 1 || drains.Load() != 1 || m.QueueBlocked(t.Context(), draftID) {
		t.Fatalf("retry did not recover saved conversation: %+v creates=%d sends=%d drains=%d err=%v", row, creates.Load(), sends.Load(), drains.Load(), err)
	}
}

func TestDraftSetupRetryRejectsUncertainAllocation(t *testing.T) {
	for _, tc := range []struct{ name, phase, steps, result, owner string }{
		{"no failure checkpoint", "failed", `{}`, "", "local"},
		{"interrupted creation", "interrupted", `{"session":"active"}`, "", "local"},
		{"created session", "failed", `{"worktree":"error"}`, `{"sessionId":"` + draftID + `"}`, "local"},
		{"allocation attempted", "failed", `{"session":"error"}`, "", "local"},
		{"remote outcome uncertain", "failed", `{"worktree":"error"}`, "", "box"},
		{"still starting", "starting", `{"worktree":"error"}`, "", "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			store := draftStore{db.SQL()}
			if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
				t.Fatal(err)
			}
			request := `{"directory":"/repo","platform":"opencode","remoteId":"` + tc.owner + `","worktree":true,"send":{"message":"saved"}}`
			if _, err := db.SQL().Exec(`UPDATE conversation_draft SET phase=?,request=?,steps=?,result=? WHERE id=?`, tc.phase, request, tc.steps, tc.result, draftID); err != nil {
				t.Fatal(err)
			}
			m := NewModule(Deps{DB: db.SQL()})
			w := httptest.NewRecorder()
			moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/retry-setup", strings.NewReader(`{}`)))
			if w.Code != http.StatusConflict {
				t.Fatalf("uncertain setup replayed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDraftSetupRetryEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, phase, request, result, steps string
		want                                bool
	}{
		{"local checkout failure", "failed", `{"worktree":true,"send":{"message":"first"}}`, "", `{"worktree":"error"}`, true},
		{"interrupted after proven failure", "interrupted", `{"worktree":true,"send":{"message":"first"}}`, "", `{"worktree":"error"}`, true},
		{"runtime failure", "failed", `{"send":{"message":"first"}}`, "", `{"platform":"error"}`, true},
		{"remote runtime before allocation", "failed", `{"platform":"r-box:opencode","send":{"message":"first"}}`, "", `{"platform":"error"}`, true},
		{"clone failure before remote call", "failed", `{"platform":"r-box:opencode","worktree":true,"send":{"message":"first"}}`, "", `{"clone":"error"}`, true},
		{"remote worktree prefix", "failed", `{"platform":"r-box:opencode","worktree":true,"send":{"message":"first"}}`, "", `{"platform":"error"}`, false},
		{"ready", "ready", `{"send":{"message":"first"}}`, "", `{"worktree":"error"}`, false},
		{"prompt attempted", "failed", `{"send":{"message":"first"}}`, "", `{"worktree":"error","prompt":"active"}`, false},
		{"bad request", "failed", `{`, "", `{"worktree":"error"}`, false},
		{"missing first payload", "failed", `{}`, "", `{"worktree":"error"}`, false},
		{"bad result", "failed", `{"send":{"message":"first"}}`, `{`, `{"worktree":"error"}`, false},
		{"empty receipt", "failed", `{"send":{"message":"first"}}`, `{}`, `{"worktree":"error"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := ConversationDraft{Phase: tc.phase, Request: json.RawMessage(tc.request), Result: json.RawMessage(tc.result)}
			if err := json.Unmarshal([]byte(tc.steps), &draft.Steps); err != nil {
				t.Fatal(err)
			}
			if got := canRetryDraftSetup(draft); got != tc.want {
				t.Fatalf("setup retryable=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestDraftSetupRetryFailuresKeepSavedPayload(t *testing.T) {
	for _, scenario := range []string{"no storage", "missing", "closed storage", "owner disconnected", "claim failure", "peer retry", "stopped workers", "restore failure"} {
		t.Run(scenario, func(t *testing.T) {
			db := statetest.Open(t, Schema)
			store := draftStore{db.SQL()}
			if err := store.save(t.Context(), draftID, json.RawMessage(`{"directory":"/repo"}`)); err != nil {
				t.Fatal(err)
			}
			payload := `{"directory":"/repo","platform":"opencode","send":{"message":"saved"}}`
			if _, err := db.SQL().Exec(`UPDATE conversation_draft SET phase='failed',request=?,steps='{"worktree":"error"}',error='checkout failed' WHERE id=?`, payload, draftID); err != nil {
				t.Fatal(err)
			}
			m := NewModule(Deps{DB: db.SQL(), Sessions: func() Sessions { return &retrySetupSessions{} },
				CreatePlatform: func(_ context.Context, _ hostsvc.Host, id, model string) (string, string, error) {
					return id, model, nil
				},
			})
			m.d.Owner = func(w http.ResponseWriter, _, _ string) (hostsvc.Host, bool) {
				if scenario == "owner disconnected" {
					http.Error(w, "disconnected", http.StatusServiceUnavailable)
					return nil, false
				}
				if scenario == "peer retry" {
					if _, err := db.SQL().Exec(`UPDATE conversation_draft SET phase='accepted' WHERE id=?`, draftID); err != nil {
						t.Fatal(err)
					}
				}
				return &retrySetupHost{}, true
			}
			want := http.StatusServiceUnavailable
			switch scenario {
			case "no storage":
				m.d.DB = nil
			case "missing":
				if _, err := db.SQL().Exec(`DELETE FROM conversation_draft WHERE id=?`, draftID); err != nil {
					t.Fatal(err)
				}
				want = http.StatusNotFound
			case "closed storage":
				if err := db.SQL().Close(); err != nil {
					t.Fatal(err)
				}
				want = http.StatusInternalServerError
			case "claim failure", "restore failure":
				phase := "accepted"
				if scenario == "restore failure" {
					phase = "failed"
				}
				if _, err := db.SQL().Exec(`CREATE TRIGGER reject_retry BEFORE UPDATE ON conversation_draft WHEN NEW.phase='` + phase + `' BEGIN SELECT RAISE(ABORT,'disk full'); END`); err != nil {
					t.Fatal(err)
				}
				want = http.StatusInternalServerError
			case "peer retry":
				want = http.StatusConflict
			}
			m.Finalize()
			w := httptest.NewRecorder()
			moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session-drafts/"+draftID+"/retry-setup", nil))
			if w.Code != want {
				t.Fatalf("status=%d, want %d: %s", w.Code, want, w.Body.String())
			}
			if scenario == "no storage" || scenario == "missing" || scenario == "closed storage" {
				return
			}
			row, err := store.get(t.Context(), draftID)
			if err != nil || string(row.Request) != payload {
				t.Fatal("retry failure lost the accepted message", row, err)
			}
			if scenario == "stopped workers" && (!row.SetupRetryable || row.Phase != "failed") {
				t.Fatal("stopped workers made a safe retry uncertain", row)
			}
		})
	}
}
