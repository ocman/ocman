package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type canonicalSnoozePlatform struct{ *fakePlatform }

func (*canonicalSnoozePlatform) CanonicalSessionIDs() bool { return true }
func (p *canonicalSnoozePlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: string(p.ID()), SourceKey: "source"}, nil
}

func TestSnoozeStaticRouteBypassesSessionPathIdentityLookup(t *testing.T) {
	for _, platform := range []string{"opencode", "claudecode", "r-box:opencode"} {
		t.Run(platform, func(t *testing.T) {
			s, registry := newSessionsTestServer(t)
			catalogue, err := sessioncatalog.New(s.stateDB)
			if err != nil {
				t.Fatal(err)
			}
			s.sessions.WithSessionCatalogue(catalogue, "owner")
			registry.Register(&canonicalSnoozePlatform{fakePlatform: &fakePlatform{id: platform}})
			if err := registry.EnableCanonicalSessions(); err != nil {
				t.Fatal(err)
			}
			owner, base := platforms.SplitPlatformID(platform)
			if owner == "" {
				owner = "owner"
			}
			source := sessioncatalog.Source{OwnerID: owner, Platform: base, SourceKey: "source"}
			if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo"}}); err != nil {
				t.Fatal(err)
			}
			bound, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: owner, Platform: base, SourceKey: "source", NativeID: "native"})
			if err != nil {
				t.Fatal(err)
			}
			id := bound.ID
			const until int64 = 9999999999999
			body, _ := json.Marshal(map[string]any{"platform": platform, "sessionId": id, "until": until})
			mux := http.NewServeMux()
			s.snoozeModule().Routes(moduleRouter{s: s, mux: mux})
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/session/snooze", strings.NewReader(string(body))))
			if response.Code != http.StatusOK {
				t.Fatalf("existing canonical session could not be snoozed: %d %s", response.Code, response.Body.String())
			}
			rows := []db.Session{{ID: id, Platform: platform}}
			if err := s.applySessionState(t.Context(), rows); err != nil {
				t.Fatal(err)
			}
			if !rows[0].Archived || rows[0].SnoozedUntil != until {
				t.Fatalf("snooze did not retain the canonical state key: %+v", rows[0])
			}
		})
	}
}
