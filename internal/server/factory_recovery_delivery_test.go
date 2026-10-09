package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestFactoryRecoveryWorkspaceConflictIsActionable(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	srv.factory = &fakeFactoryService{err: fmt.Errorf("%w by Issue claude-code-provider.1.3.105. Resume after that Issue releases the workspace", model.ErrRecoveryWorkspaceBusy)}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/recovery-gates/claude-code-provider.1.23/resume", strings.NewReader(`{"response":"Continue"}`))
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	srv.handleFactoryRecoveryGate(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "claude-code-provider.1.3.105") {
		t.Fatalf("resume conflict = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFactoryRecoveryConfirmsDeliveryAfterTimeout(t *testing.T) {
	for _, tc := range []struct {
		name      string
		accepted  bool
		readError bool
		missing   bool
	}{
		{name: "accepted", accepted: true},
		{name: "not accepted"},
		{name: "confirmation read failed", readError: true},
		{name: "confirmation session unavailable", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := &platforms.SessionDetail{}
			platform := &fakePlatform{id: "opencode"}
			sends := 0
			platform.sessionDetailFn = func(string) (*platforms.SessionDetail, error) {
				if sends > 0 && tc.readError {
					return nil, errors.New("read failed")
				}
				if sends > 0 && tc.missing {
					return nil, nil
				}
				return detail, nil
			}
			platform.sendMessageFn = func(req platforms.SendMessageRequest) error {
				sends++
				if tc.accepted {
					detail.Parts = []db.Part{{Data: []byte(req.Message)}}
				}
				return context.DeadlineExceeded
			}
			registry := platforms.NewRegistry()
			registry.Register(platform)
			launcher := factoryImplementationLauncher{server: New(nil, nil, "", registry, nil)}
			session := factory.PlanningSession{Platform: "opencode", ID: "session"}
			err := launcher.ResumeImplementationSession(t.Context(), session, "gate", "Continue")
			if tc.accepted && err != nil {
				t.Fatalf("accepted recovery returned an error: %v", err)
			}
			if !tc.accepted && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unconfirmed recovery lost delivery error: %v", err)
			}
			if tc.accepted {
				if err := launcher.ResumeImplementationSession(t.Context(), session, "gate", "Continue"); err != nil || sends != 1 {
					t.Fatalf("recovery duplicated: sends=%d, err=%v", sends, err)
				}
			}
		})
	}
}
