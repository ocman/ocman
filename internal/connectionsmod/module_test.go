package connectionsmod

import (
	"context"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
)

type owner struct {
	hostsvc.Host
	hostsvc.ConnectionHost
	checks  int
	updated hostsvc.ConnectionUpdate
}

func (o *owner) InventoryPlatformConnections(context.Context) (hostsvc.ConnectionInventory, error) {
	return hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_work", Kind: "codex", Name: "Work", Status: "unchecked"}}}, nil
}
func (o *owner) CheckPlatformConnection(context.Context, hostsvc.ConnectionCheckRequest) (hostsvc.ConnectionCheck, error) {
	o.checks++
	return hostsvc.ConnectionCheck{ReservationID: "stp_kept"}, platformconnections.ErrUnavailable
}
func (o *owner) UpdatePlatformConnection(_ context.Context, r hostsvc.ConnectionUpdate) error {
	o.updated = r
	return nil
}

func TestOwnerRequiredAndDisconnectedFailsClosed(t *testing.T) {
	calls := 0
	m := moduletest.Mount(NewModule(Deps{ResolveOwner: func(r *http.Request) (hostsvc.Host, error) {
		calls++
		if r.URL.Query().Get("remoteId") != "remote" {
			t.Fatal("owner changed")
		}
		return nil, platforms.ErrRemoteOffline
	}}))
	for _, tt := range []struct {
		path   string
		status int
	}{{"/api/platform-connections", 400}, {"/api/platform-connections?remoteId=remote", 503}} {
		resp := moduletest.Do(m, "GET", tt.path, "", "")
		if resp.Code != tt.status {
			t.Fatalf("%s: %d %s", tt.path, resp.Code, resp.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestConnectionAPIKeepsFailedSetupReceipt(t *testing.T) {
	o := &owner{}
	m := moduletest.Mount(NewModule(Deps{ResolveOwner: func(*http.Request) (hostsvc.Host, error) { return o, nil }}))
	resp := moduletest.Do(m, "POST", "/api/platform-connections/check?remoteId=local", `{"kind":"codex","configuration":"isolated"}`, "")
	if resp.Code != 409 || resp.Body.String() != `{"error":"Setup check failed. Retry with this reservation.","reservationId":"stp_kept"}`+"\n" {
		t.Fatalf("%d %s", resp.Code, resp.Body.String())
	}
	resp = moduletest.Do(m, "PATCH", "/api/platform-connections/cnx_actual?remoteId=local", `{"id":"cnx_other","enabled":false}`, "")
	if resp.Code != 200 || o.updated.ID != "cnx_actual" || o.updated.Enabled == nil || *o.updated.Enabled {
		t.Fatalf("%d %+v", resp.Code, o.updated)
	}
	resp = moduletest.Do(m, "PUT", "/api/platform-connections?remoteId=local", `{}`, "")
	if resp.Code != 405 {
		t.Fatal(resp.Code)
	}
	resp = moduletest.Do(m, "POST", "/api/platform-connections?remoteId=local", `{"name":"Work","directory":"/untrusted"}`, "")
	if resp.Code != 400 {
		t.Fatal(resp.Code)
	}
}

func TestConnectionModuleAppliesLocalGuard(t *testing.T) {
	r := moduletest.Router{Mux: http.NewServeMux(), Localhost: func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "forbidden", http.StatusForbidden) }
	}}
	NewModule(Deps{}).Routes(r)
	if resp := moduletest.Do(r.Mux, "POST", "/api/platform-connections/check?remoteId=local", `{}`, ""); resp.Code != 403 {
		t.Fatal(resp.Code)
	}
}
