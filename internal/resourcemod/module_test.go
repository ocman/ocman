package resourcemod

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type resourceHost struct {
	hostsvc.Host
	err      error
	snapshot hostsvc.ResourceSnapshot
}

func (h resourceHost) RemoteID() string { return "local" }

func (h resourceHost) Resources(context.Context) (hostsvc.ResourceSnapshot, error) {
	if h.snapshot.Processes != nil {
		return h.snapshot, h.err
	}
	return hostsvc.ResourceSnapshot{Processes: []hostsvc.ResourceProcess{}, History: []hostsvc.ResourcePoint{}}, h.err
}

func TestResourceRouteSessionLink(t *testing.T) {
	host := resourceHost{snapshot: hostsvc.ResourceSnapshot{Processes: []hostsvc.ResourceProcess{{PID: 1, Command: "agent --session-id native"}}}}
	m := &Module{
		ResolveOwner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return host, true },
		Sessions: func(context.Context, string, int, int) ([]sessioncatalog.Session, error) {
			return []sessioncatalog.Session{{ID: "canonical", NativeID: "native"}}, nil
		},
	}
	response := moduletest.Do(moduletest.Mount(m), "GET", "/api/resources", "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"sessionId":"canonical"`) {
		t.Fatal(response.Body.String())
	}
	if host.snapshot.Processes[0].SessionID != "" {
		t.Fatal("mutated host cache")
	}
}

func TestResourceRoutes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		host     hostsvc.Host
		resolved bool
		status   int
	}{
		{"success", resourceHost{}, true, 200},
		{"unsupported", struct{ hostsvc.Host }{}, true, 503},
		{"disconnected", nil, false, 503},
		{"failed", resourceHost{err: errors.New("secret diagnostic")}, true, 503},
		{"older owner", resourceHost{err: fmt.Errorf("remote: %w", platforms.ErrUnsupported)}, true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Module{ResolveOwner: func(w http.ResponseWriter, dir, owner string) (hostsvc.Host, bool) {
				if owner != "named-owner" || dir != "" {
					t.Fatal("owner scope lost")
				}
				if !tc.resolved {
					http.Error(w, "disconnected", http.StatusServiceUnavailable)
				}
				return tc.host, tc.resolved
			}}
			if m.Name() != "resources" {
				t.Fatal(m.Name())
			}
			mux := moduletest.Mount(m)
			response := moduletest.Do(mux, "GET", "/api/resources?remoteId=named-owner", "", "")
			if response.Code != tc.status || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			if tc.name == "older owner" && !strings.Contains(response.Body.String(), "unavailable on this host") {
				t.Fatalf("older owner reported as read failure: %s", response.Body.String())
			}
			if tc.status == 200 && !strings.Contains(response.Body.String(), `"processes":[]`) {
				t.Fatal(response.Body.String())
			}
			if got := moduletest.Do(mux, "POST", "/api/resources", "", ""); got.Code != 405 {
				t.Fatal(got.Code)
			}
		})
	}
}
