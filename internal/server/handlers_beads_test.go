package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type beadsHost struct {
	hostsvc.Host
	id     string
	gotDir string
	status hostsvc.BeadsStatus
	err    error
}

func (h *beadsHost) RemoteID() string { return h.id }
func (h *beadsHost) BeadsStatus(_ context.Context, dir string) (hostsvc.BeadsStatus, error) {
	h.gotDir = dir
	return h.status, h.err
}

func TestHandleProjectBeadsStatusRoutesToOwningHost(t *testing.T) {
	srv := testServer(t)
	local := &beadsHost{id: "local"}
	remote := &beadsHost{id: "abc", status: hostsvc.BeadsStatus{Available: true, Tickets: []hostsvc.BeadsTicket{{ID: "bd-1", Title: "One", Status: "open", Priority: 1}}}}
	srv.hostRouter = hostsvc.NewRouter(local)
	srv.hostRouter.RegisterRemote("abc", remote)

	req := httptest.NewRequest(http.MethodGet, "/api/project/beads-status?dir=/repo&remoteId=abc", nil)
	rr := httptest.NewRecorder()
	srv.handleProjectBeadsStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if remote.gotDir != "/repo" {
		t.Fatalf("remote host got dir %q", remote.gotDir)
	}
	if local.gotDir != "" {
		t.Fatalf("local host unexpectedly executed for %q", local.gotDir)
	}
	var got hostsvc.BeadsStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Tickets) != 1 {
		t.Fatalf("response %+v", got)
	}
}

func TestHandleProjectBeadsStatusResponses(t *testing.T) {
	tests := []struct {
		name string
		url  string
		host *beadsHost
		code int
		want *hostsvc.BeadsStatus
	}{
		{name: "rejects relative directory", url: "/api/project/beads-status?dir=relative", code: http.StatusBadRequest},
		{name: "rejects unknown remote owner", url: "/api/project/beads-status?dir=/repo&remoteId=abc", host: &beadsHost{id: "local", err: errors.New("must not run")}, code: http.StatusServiceUnavailable},
		{name: "unavailable", url: "/api/project/beads-status?dir=/repo", host: &beadsHost{id: "local"}, code: http.StatusOK},
		{name: "available error", url: "/api/project/beads-status?dir=/repo", host: &beadsHost{id: "local", status: hostsvc.BeadsStatus{Available: true, Error: "status_unavailable"}}, code: http.StatusOK, want: &hostsvc.BeadsStatus{Available: true, Error: "status_unavailable"}},
		{name: "host failure", url: "/api/project/beads-status?dir=/repo", host: &beadsHost{id: "local", err: errors.New("boom")}, code: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := testServer(t)
			if tt.host != nil {
				srv.hostRouter = hostsvc.NewRouter(tt.host)
			}
			rr := httptest.NewRecorder()
			srv.handleProjectBeadsStatus(rr, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if rr.Code != tt.code {
				t.Fatalf("status %d, want %d: %s", rr.Code, tt.code, rr.Body.String())
			}
			if tt.host != nil && tt.code != http.StatusOK && tt.code != http.StatusBadGateway && tt.host.gotDir != "" {
				t.Fatalf("local host unexpectedly executed for %q", tt.host.gotDir)
			}
			if tt.want != nil {
				var got hostsvc.BeadsStatus
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || !reflect.DeepEqual(got, *tt.want) {
					t.Fatalf("response=%+v err=%v, want %+v", got, err, *tt.want)
				}
			}
		})
	}
}
