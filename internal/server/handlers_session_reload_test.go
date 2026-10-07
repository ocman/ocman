package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type reloadTestHost struct {
	hostsvc.Host
	calls int
	err   error
}

func (h *reloadTestHost) ReloadOpencode(context.Context) error {
	h.calls++
	return h.err
}

func TestReloadOpencodeEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, owner, method, addr, origin string
		disconnected, missing             bool
		err                               error
		status, localCalls, remoteCalls   int
	}{
		{name: "busy local", status: 204, localCalls: 1},
		{name: "explicit local", owner: "local", status: 204, localCalls: 1},
		{name: "remote", owner: "remote-1", status: 204, remoteCalls: 1},
		{name: "disconnected", owner: "remote-1", disconnected: true, status: 503},
		{name: "non loopback", addr: "192.0.2.1:1234", status: 403},
		{name: "foreign origin", origin: "https://evil.example", status: 403},
		{name: "wrong method", method: "GET", status: 405},
		{name: "missing session", missing: true, status: 404},
		{name: "unsupported", err: platforms.ErrUnsupported, status: 501, localCalls: 1},
		{name: "upstream error", err: errors.New("upstream failed"), status: 502, localCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			local, remote := &reloadTestHost{err: tc.err}, &reloadTestHost{err: tc.err}
			srv.hostRouter = hostsvc.NewRouter(local)
			if !tc.disconnected {
				srv.hostRouter.RegisterRemote("remote-1", remote)
			}
			reg.Register(&fakePlatform{
				id: "opencode", sessions: []db.Session{{ID: "s1"}},
				sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
					if tc.missing {
						return nil, nil
					}
					return &platforms.SessionDetail{Session: &db.Session{ID: "s1", Directory: "/shared/path", RemoteID: tc.owner, Status: db.StatusBusy}}, nil
				},
			})
			method := tc.method
			if method == "" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, "/api/session/s1/reload-opencode", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			if tc.addr != "" {
				req.RemoteAddr = tc.addr
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			srv.dispatchSessionSubpath(rr, req)
			if rr.Code != tc.status || local.calls != tc.localCalls || remote.calls != tc.remoteCalls {
				t.Fatalf("status=%d body=%s local=%d remote=%d", rr.Code, rr.Body, local.calls, remote.calls)
			}
		})
	}
}
