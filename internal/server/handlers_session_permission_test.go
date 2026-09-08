package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionPermissionOnlyInspectsPromptsForFactoryApprovals(t *testing.T) {
	for _, tt := range []struct {
		name, reply        string
		factorySession     bool
		wantLists, wantEsc int
		wantResolve        bool
		wantReply          bool
		wantStatus         int
		resolveErr         error
	}{
		{"ordinary approval", "once", false, 0, 0, false, true, http.StatusNoContent, nil},
		{"Factory denial", "reject", true, 0, 0, false, true, http.StatusNoContent, nil},
		{"Factory approval once", "once", true, 1, 1, true, false, http.StatusOK, nil},
		{"Factory approval always", "always", true, 1, 1, true, false, http.StatusOK, nil},
		{"invalid Factory approval", "maybe", true, 0, 0, false, false, http.StatusBadRequest, nil},
		{"Factory approval delivery failure", "once", true, 1, 1, true, false, http.StatusInternalServerError, errors.New("delivery failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lists := 0
			var replied platforms.RespondPermissionRequest
			platform := &fakePlatform{id: "fake"}
			platform.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) {
				lists++
				return []platforms.LivePrompt{{"id": "permission-1", "permission": "external_directory", "patterns": []any{"/outside"}}}, nil
			}
			platform.respondPermissionFn = func(req platforms.RespondPermissionRequest) error { replied = req; return nil }
			srv := newPermissionRulesTestServer(t, platform)
			factorySvc := &fakeFactoryService{implementationSession: tt.factorySession, authorityErr: tt.resolveErr}
			srv.factory = factorySvc
			mux, err := srv.routes()
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/session/sess-1/permissions/permission-1?platform=fake", strings.NewReader(`{"reply":"`+tt.reply+`"}`))
			req.RemoteAddr = "127.0.0.1:1"
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if lists != tt.wantLists || factorySvc.escalationCalls != tt.wantEsc {
				t.Fatalf("lists/escalations = %d/%d, want %d/%d", lists, factorySvc.escalationCalls, tt.wantLists, tt.wantEsc)
			}
			if (factorySvc.authorityGateID != "") != tt.wantResolve || (tt.wantResolve && factorySvc.authorityAction != "approve") {
				t.Fatalf("authority resolution = %q/%q, want resolve=%v", factorySvc.authorityGateID, factorySvc.authorityAction, tt.wantResolve)
			}
			if (replied.PermissionID != "") != tt.wantReply || (tt.wantReply && replied.Reply != tt.reply) {
				t.Fatalf("reply = %#v, want direct=%v", replied, tt.wantReply)
			}
		})
	}
}
