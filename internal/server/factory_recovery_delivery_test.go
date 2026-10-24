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

func TestFactoryRecoveryResumeErrorsAreActionable(t *testing.T) {
	for _, tc := range []struct {
		name, gate string
		err        error
		want       int
		wantBody   string
	}{
		{"workspace conflict", "claude-code-provider.1.23", fmt.Errorf("%w by Issue claude-code-provider.1.3.105. Resume after that Issue releases the workspace", model.ErrRecoveryWorkspaceBusy), http.StatusConflict, "claude-code-provider.1.3.105"},
		{"invalid checkpoint", "gate", fmt.Errorf("validate resumed Factory workspace: %w: factory worktree has uncommitted changes", factory.ErrInvalidRequest), http.StatusBadRequest, "uncommitted changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(nil, nil, "", nil, nil)
			useFactory(srv, &fakeFactoryService{err: tc.err})
			req := httptest.NewRequest(http.MethodPost, "/api/factory/recovery-gates/"+tc.gate+"/resume", strings.NewReader(`{"response":"Continue"}`))
			req.RemoteAddr = "127.0.0.1:1"
			rec := httptest.NewRecorder()
			serveFactory(srv, rec, req)
			if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("resume = %d: %s", rec.Code, rec.Body.String())
			}
		})
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
			launcher := New(nil, nil, "", registry, nil).factoryModule().ImplementationLauncher()
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
