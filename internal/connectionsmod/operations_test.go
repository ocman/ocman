package connectionsmod

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type operationOwner struct {
	hostsvc.Host
	hostsvc.ConnectionSetupHost
	err                 error
	operation, id, name string
	enabled             bool
	check               hostsvc.ConnectionCheckRequest
}

func (o *operationOwner) InventoryPlatformConnections(context.Context) (hostsvc.ConnectionInventory, error) {
	o.operation = "list"
	return hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_work", Kind: "codex"}}}, o.err
}
func (o *operationOwner) CheckPlatformConnection(_ context.Context, r hostsvc.ConnectionCheckRequest) (hostsvc.ConnectionCheck, error) {
	o.operation, o.check = "check", r
	return hostsvc.ConnectionCheck{Status: "sign-in-required"}, o.err
}
func (o *operationOwner) SavePlatformConnection(_ context.Context, id, name string, enabled bool) (platformconnections.Connection, error) {
	o.operation, o.id, o.name, o.enabled = "save", id, name, enabled
	return platformconnections.Connection{ID: "cnx_work"}, o.err
}
func (o *operationOwner) UpdatePlatformConnection(_ context.Context, r hostsvc.ConnectionUpdate) error {
	o.operation, o.id = "update", r.ID
	return o.err
}
func (o *operationOwner) RemovePlatformConnection(_ context.Context, id string) error {
	o.operation, o.id = "remove", id
	return o.err
}
func (o *operationOwner) CancelPlatformConnectionSetup(_ context.Context, id string) error {
	o.operation, o.id = "cancel", id
	return o.err
}
func (o *operationOwner) CompletePlatformConnectionOnboarding(context.Context) error {
	o.operation = "onboarding"
	return o.err
}

var connectionOperations = []struct{ operation, method, path, body, response string }{
	{"list", "GET", "/api/platform-connections", "", `"cnx_work"`},
	{"check", "POST", "/api/platform-connections/check", `{"kind":"codex","reservationId":"stp_work"}`, `"sign-in-required"`},
	{"save", "POST", "/api/platform-connections", `{"reservationId":"stp_work","name":"Work","enabled":true}`, `"saved":true`},
	{"update", "PATCH", "/api/platform-connections/cnx_work", `{"id":"cnx_untrusted","default":true}`, `"saved":true`},
	{"remove", "DELETE", "/api/platform-connections/cnx_work", "", `"removed":true`},
	{"cancel", "DELETE", "/api/platform-connections/setup/stp_work", "", `"canceled":true`},
	{"onboarding", "POST", "/api/platform-connections/onboarding", "", `"saved":true`},
}

func TestConnectionOperationsForwardExactOwnerAndReceipts(t *testing.T) {
	for _, tc := range connectionOperations {
		t.Run(tc.operation, func(t *testing.T) {
			o := &operationOwner{}
			m := NewModule(Deps{ResolveOwner: func(r *http.Request) (hostsvc.Host, error) {
				if r.URL.Query().Get("remoteId") != "remote-work" {
					t.Fatal("owner lost")
				}
				return o, nil
			}})
			if m.Name() != "platform-connections" {
				t.Fatal(m.Name())
			}
			response := moduletest.Do(moduletest.Mount(m), tc.method, tc.path+"?remoteId=remote-work", tc.body, "")
			if response.Code != http.StatusOK || o.operation != tc.operation || !strings.Contains(response.Body.String(), tc.response) {
				t.Fatalf("operation=%s: %d %s", o.operation, response.Code, response.Body.String())
			}
			switch tc.operation {
			case "check":
				if o.check.ReservationID != "stp_work" || o.check.Kind != "codex" {
					t.Fatal(o.check)
				}
			case "save":
				if o.id != "stp_work" || o.name != "Work" || !o.enabled {
					t.Fatalf("wrong saved receipt: %+v", o)
				}
			case "update", "remove":
				if o.id != "cnx_work" {
					t.Fatal(o.id)
				}
			case "cancel":
				if o.id != "stp_work" {
					t.Fatal(o.id)
				}
			}
		})
	}
}

func TestConnectionOperationFailuresAreSanitized(t *testing.T) {
	for _, tc := range connectionOperations {
		t.Run(tc.operation, func(t *testing.T) {
			o := &operationOwner{err: errors.New("private native error")}
			m := moduletest.Mount(NewModule(Deps{ResolveOwner: func(*http.Request) (hostsvc.Host, error) { return o, nil }}))
			r := moduletest.Do(m, tc.method, tc.path+"?remoteId=local", tc.body, "")
			if r.Code != http.StatusInternalServerError || strings.Contains(r.Body.String(), "private native") {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
		})
	}
}

func TestConnectionOperationRequiresOwnerBeforeEffects(t *testing.T) {
	for _, tc := range connectionOperations {
		for _, configured := range []bool{false, true} {
			deps := Deps{}
			if configured {
				deps.ResolveOwner = func(*http.Request) (hostsvc.Host, error) { t.Fatal("missing owner reached resolver"); return nil, nil }
			}
			path := tc.path
			want := http.StatusBadRequest
			if !configured {
				path += "?remoteId=local"
				want = http.StatusServiceUnavailable
			}
			r := moduletest.Do(moduletest.Mount(NewModule(deps)), tc.method, path, tc.body, "")
			if r.Code != want {
				t.Fatalf("%s: %d %s", tc.operation, r.Code, r.Body.String())
			}
		}
	}
}

func TestConnectionBodyValidationPreventsOwnerMutations(t *testing.T) {
	for _, tc := range connectionOperations {
		if tc.operation != "check" && tc.operation != "save" && tc.operation != "update" {
			continue
		}
		o := &operationOwner{}
		m := moduletest.Mount(NewModule(Deps{ResolveOwner: func(*http.Request) (hostsvc.Host, error) { return o, nil }}))
		for _, body := range []string{"{", strings.Repeat("x", 4097)} {
			r := moduletest.Do(m, tc.method, tc.path+"?remoteId=local", body, "")
			if r.Code < 400 || o.operation != "" {
				t.Fatalf("%s reached mutation: %d", tc.operation, r.Code)
			}
		}
	}
}

func TestConnectionErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		want int
	}{
		{codes.InvalidArgument, 400}, {codes.NotFound, 404}, {codes.FailedPrecondition, 409}, {codes.Unimplemented, 501}, {codes.Unavailable, 503},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			m := moduletest.Mount(NewModule(Deps{ResolveOwner: func(*http.Request) (hostsvc.Host, error) { return nil, status.Error(tc.code, "private RPC detail") }}))
			r := moduletest.Do(m, "GET", "/api/platform-connections?remoteId=remote", "", "")
			if r.Code != tc.want || strings.Contains(r.Body.String(), "private") {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
		})
	}
	m := moduletest.Mount(NewModule(Deps{ResolveOwner: func(*http.Request) (hostsvc.Host, error) { return nil, nil }}))
	if r := moduletest.Do(m, "GET", "/api/platform-connections?remoteId=legacy", "", ""); r.Code != http.StatusNotImplemented {
		t.Fatal(r.Code)
	}
	for _, err := range []error{platformconnections.ErrNotFound, platformconnections.ErrLimit, platforms.ErrUnsupported} {
		m = moduletest.Mount(NewModule(Deps{ResolveOwner: func(*http.Request) (hostsvc.Host, error) { return nil, err }}))
		if r := moduletest.Do(m, "GET", "/api/platform-connections?remoteId=local", "", ""); r.Code < 400 {
			t.Fatal(r.Code)
		}
	}
}
