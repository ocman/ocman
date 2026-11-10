package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type connectionEventPlatform struct {
	*compactEventPlatform
	owner, source string
}

func (p *connectionEventPlatform) CanonicalSources() (string, string, []string) {
	return p.owner, string(p.ID()), []string{p.source}
}

func eventConnection(t *testing.T, reg *platforms.Registry, key, owner, source string) *connectionEventPlatform {
	t.Helper()
	p := &connectionEventPlatform{compactEventPlatform: &compactEventPlatform{fakePlatform: &fakePlatform{PlatformID: "claudecode"}}, owner: owner, source: source}
	if err := reg.RegisterConnection(platforms.ID(key), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func eventCatalogueRow(t *testing.T, s *Server, owner, source string) string {
	t.Helper()
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	binding := sessioncatalog.Binding{OwnerID: owner, Platform: "claudecode", SourceKey: source, NativeID: "native"}
	_, err = catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: owner, Platform: binding.Platform, SourceKey: source}, []sessioncatalog.NativeSession{{ID: binding.NativeID, Directory: "/project", Title: "title"}})
	if err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func TestPublicSessionEventUsesExactCanonicalSource(t *testing.T) {
	for _, event := range []string{"ocman.session.changed", "ocman.session.status", "ocman.session.activity", "ocman.session.idle", "ocman.permission.resolved"} {
		t.Run(event, func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			eventConnection(t, reg, "cnx_sibling", "local", "sibling")
			eventConnection(t, reg, "cnx_expected", "local", "retained")
			id := eventCatalogueRow(t, s, "local", "retained")
			original, _ := json.Marshal(map[string]any{"platform": "claudecode", "sessionID": id, "session": db.Session{ID: id, Platform: "claudecode"}, "patch": map[string]any{"platform": "claudecode", "title": "new", "status": "busy", "backgroundTaskCount": 2}})
			data, ok := s.publicSessionEvent(event, original)
			if !ok {
				t.Fatal("exact source event rejected")
			}
			var got struct {
				Platform string
				Session  db.Session
				Patch    db.Session
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Platform != "cnx_expected" || got.Session.Platform != "cnx_expected" || got.Patch.Platform != "cnx_expected" || got.Patch.Title != "new" || got.Patch.BackgroundTaskCount != 2 {
				t.Fatalf("wrong public identity: %s", data)
			}
			var native map[string]any
			_ = json.Unmarshal(original, &native)
			if native["platform"] != "claudecode" {
				t.Fatal("mutated native payload")
			}
		})
	}
}

func TestPublicSessionEventRejectsUnknownAndAmbiguousSources(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "ambiguous"}[ambiguous], func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			_, stop := s.broadcastHub.subscribe()
			defer stop()
			eventConnection(t, reg, "cnx_sibling", "local", "other")
			if ambiguous {
				eventConnection(t, reg, "cnx_a", "local", "exact")
				eventConnection(t, reg, "cnx_b", "local", "exact")
			}
			id := eventCatalogueRow(t, s, "local", "exact")
			data, _ := json.Marshal(map[string]string{"platform": "claudecode", "sessionID": id})
			if got, ok := s.publicSessionEvent("ocman.session.changed", data); ok {
				t.Fatalf("unresolved source published: %s", got)
			}
			if s.enqueueSidebarChange("claudecode", id) {
				t.Fatal("unresolved source queued")
			}
		})
	}
}

func TestEnqueueSidebarChangeUsesConnectionKey(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	p := eventConnection(t, reg, "cnx_expected", "local", "exact")
	id := eventCatalogueRow(t, s, "local", "exact")
	updates := s.sessionUpdatesModule()
	if err := updates.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); updates.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	sub, stop := s.broadcastHub.subscribe()
	defer stop()
	if !s.enqueueSidebarChange("claudecode", id) {
		t.Fatal("native callback did not queue exact connection")
	}
	select {
	case ev := <-sub.ch:
		var got struct {
			Platform string
			Session  *db.Session
		}
		if err := json.Unmarshal(ev.data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Platform != "cnx_expected" || got.Session == nil || got.Session.Platform != "cnx_expected" {
			t.Fatalf("compact row lost connection: %s", ev.data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no compact event")
	}
	if p.reads.Load() != 1 {
		t.Fatal("missing compact read")
	}
}

func TestPublicSessionEventPreservesLegacyAndUnrelatedEvents(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{PlatformID: "legacy"})
	eventConnection(t, reg, "cnx_claude", "local", "exact")
	for _, tc := range []struct{ event, data string }{
		{"ocman.session.changed", `{"platform":"legacy","sessionID":"native-id","patch":{"title":"title"}}`},
		{"ocman.session.changed", `{"platform":"legacy","sessionID":"native-id","patch":null}`},
		{"ocman.session.changed", `{`},
		{"ocman.session.changed", `null`},
		{"ocman.session.changed", `{"platform":"legacy"}`},
		{"ocman.session.idle", `{"sessionID":"legacy-id"}`},
		{"ocman.projects.changed", `{"platform":"claudecode","sessionID":"missing"}`},
	} {
		// A platform-less legacy idle is preserved without named connections.
		server := s
		if tc.event == "ocman.session.idle" {
			server = &Server{}
		}
		got, ok := server.publicSessionEvent(tc.event, []byte(tc.data))
		if !ok {
			t.Fatalf("legacy event rejected: %s", tc.data)
		}
		var wantFields, gotFields any
		wantErr := json.Unmarshal([]byte(tc.data), &wantFields)
		gotErr := json.Unmarshal(got, &gotFields)
		if wantErr != nil || gotErr != nil {
			if string(got) != tc.data {
				t.Fatalf("malformed legacy event changed: %s", got)
			}
			continue
		}
		wantJSON, _ := json.Marshal(wantFields)
		gotJSON, _ := json.Marshal(gotFields)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("legacy event changed: %s", got)
		}
	}
}

func TestPublicSessionEventRejectsMissingProvenanceAndWrongConnection(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	eventConnection(t, reg, "cnx_sibling", "local", "sibling")
	eventConnection(t, reg, "cnx_exact", "local", "exact")
	id := eventCatalogueRow(t, s, "local", "exact")
	for _, tc := range []struct{ platform, id string }{
		{"cnx_sibling", id},
		{"opencode", id},
		{"r-remote:claudecode", id},
		{"claudecode", "missing"},
		{"cnx_exact", "missing"},
		{"", "missing"},
	} {
		data, _ := json.Marshal(map[string]string{"platform": tc.platform, "sessionID": tc.id})
		if got, ok := s.publicSessionEvent("ocman.session.changed", data); ok {
			t.Fatalf("unproven event published: %s", got)
		}
	}
}

func TestPublicSessionEventUsesRemoteOwnerAndAddsMissingPlatform(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	eventConnection(t, reg, "cnx_local", "local", "exact")
	eventConnection(t, reg, "r-remote:cnx_exact", "remote", "exact")
	id := eventCatalogueRow(t, s, "remote", "exact")
	for _, platform := range []string{"", "r-remote:claudecode", "r-remote:cnx_exact"} {
		data, _ := json.Marshal(map[string]string{"platform": platform, "sessionID": id})
		got, ok := s.publicSessionEvent("ocman.session.idle", data)
		var fields map[string]string
		if err := json.Unmarshal(got, &fields); err != nil {
			t.Fatal(err)
		}
		if !ok || fields["platform"] != "r-remote:cnx_exact" {
			t.Fatalf("wrong owner: %s", got)
		}
	}
}

func TestPublicSessionEventPreservesCanonicalLegacyRegistration(t *testing.T) {
	for _, sibling := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-only", true: "named-sibling"}[sibling], func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			reg.Register(&fakePlatform{PlatformID: "claudecode"})
			if sibling {
				eventConnection(t, reg, "cnx_other", "local", "other")
			}
			id := eventCatalogueRow(t, s, "local", "legacy-source")
			data, _ := json.Marshal(map[string]string{"platform": "claudecode", "sessionID": id})
			got, ok := s.publicSessionEvent("ocman.session.changed", data)
			if ok == sibling {
				t.Fatalf("legacy publication allowed=%v, named sibling=%v: %s", ok, sibling, got)
			}
			if ok {
				var fields map[string]string
				if err := json.Unmarshal(got, &fields); err != nil {
					t.Fatal(err)
				}
				if fields["platform"] != "claudecode" {
					t.Fatalf("legacy identity changed: %s", got)
				}
			}
		})
	}
}

type remoteNamedEventPlatform struct{ *fakePlatform }

func (*remoteNamedEventPlatform) NativeKind() platforms.ID { return "claudecode" }

func TestPublicSessionEventRejectsUnresolvedRemoteNamedAdapter(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	reg.Register(&remoteNamedEventPlatform{fakePlatform: &fakePlatform{PlatformID: "r-remote:cnx_account"}})
	for _, id := range []string{"ocm_00000000-0000-4000-8000-000000000001", eventCatalogueRow(t, s, "remote", "unresolved")} {
		for _, platform := range []string{"r-remote:claudecode", "r-remote:cnx_account", ""} {
			data, _ := json.Marshal(map[string]string{"platform": platform, "sessionID": id})
			if got, ok := s.publicSessionEvent("ocman.session.changed", data); ok {
				t.Fatalf("unresolved remote source published: %s", got)
			}
		}
	}
}

type legacyReadSourcesEventPlatform struct {
	*fakePlatform
	sources []string
}

func (p *legacyReadSourcesEventPlatform) ReadSourceKeys() []string { return p.sources }

func TestPublicSessionEventLegacySourceAllowlist(t *testing.T) {
	for _, source := range []string{"legacy-source", "retained-source", "unknown-source"} {
		t.Run(source, func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			reg.Register(&legacyReadSourcesEventPlatform{fakePlatform: &fakePlatform{PlatformID: "claudecode"}, sources: []string{"legacy-source", "retained-source"}})
			id := eventCatalogueRow(t, s, "local", source)
			_, ok := s.publicSessionPlatform("claudecode", id)
			if ok != (source != "unknown-source") {
				t.Fatalf("legacy source %q publication allowed=%v", source, ok)
			}
		})
	}
}

func TestPublicSessionEventNamedConflictsAreOwnerScoped(t *testing.T) {
	for _, remoteLegacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "local-legacy", true: "remote-legacy"}[remoteLegacy], func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			owner, platform := "local", "claudecode"
			if remoteLegacy {
				owner, platform = "remote", "r-remote:claudecode"
				reg.Register(&remoteNamedEventPlatform{fakePlatform: &fakePlatform{PlatformID: platform}})
				eventConnection(t, reg, "cnx_local", "local", "other")
			} else {
				reg.Register(&fakePlatform{PlatformID: "claudecode"})
				reg.Register(&remoteNamedEventPlatform{fakePlatform: &fakePlatform{PlatformID: "r-remote:cnx_account"}})
			}
			id := eventCatalogueRow(t, s, owner, "legacy-source")
			got, ok := s.publicSessionPlatform(platform, id)
			if !ok || got != platform {
				t.Fatalf("another owner's named adapter suppressed legacy platform %q: %q, %v", platform, got, ok)
			}
			got, ok = s.publicSessionPlatform(platform, "legacy-native-id")
			if !ok || got != platform {
				t.Fatalf("another owner's named adapter suppressed native legacy event: %q, %v", got, ok)
			}
		})
	}
}

func TestBroadcastGlobalEventPublishesConnectionIdentity(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	eventConnection(t, reg, "cnx_expected", "local", "exact")
	id := eventCatalogueRow(t, s, "local", "exact")
	sub, stop := s.broadcastHub.subscribe()
	defer stop()
	s.onSessionStatus("claudecode", id, db.StatusBusy, 0)
	select {
	case event := <-sub.ch:
		var payload struct {
			Platform  string
			SessionID string
		}
		if err := json.Unmarshal(event.data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Platform != "cnx_expected" || payload.SessionID != id {
			t.Fatalf("native identity escaped on wire: %s", event.data)
		}
	default:
		t.Fatal("exact connection event was not published")
	}
}
