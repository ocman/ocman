package owned

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type sidebarNative struct {
	*nativePlatform
	t            *testing.T
	observations map[string]platforms.LiveState
}

func (p *sidebarNative) CachedSessionObservations() map[string]platforms.LiveState {
	if p.observations != nil {
		return p.observations
	}
	return map[string]platforms.LiveState{"native": {Status: "busy"}}
}

func (p *sidebarNative) EnrichStoredSessions(context.Context, []db.Session, map[string]string) error {
	p.t.Fatal("sidebar invoked native enrichment")
	return nil
}
func (p *sidebarNative) Sessions(context.Context, string, int64) ([]db.Session, error) {
	p.t.Fatal("sidebar enumerated native history")
	return nil, nil
}

func TestSidebarDoesNotReadSummariesOrCallNativeBrowsing(t *testing.T) {
	a, p, id := storedFixture(t)
	a.native = &sidebarNative{nativePlatform: p, t: t}
	t.Cleanup(func() {
		if a.sidebarReader != nil {
			_ = a.sidebarReader.Close()
		}
	})
	reader, ok := any(a).(platforms.SidebarReader)
	if !ok {
		t.Fatal("canonical adapter has no compact sidebar read path")
	}
	rows, err := reader.SidebarSessions(context.Background(), platforms.SidebarQuery{})
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].Title != "stored" {
		t.Fatalf("sidebar lost stored metadata: %+v %v", rows, err)
	}
}

func TestConversationHeaderDoesNotRunNativeEnrichment(t *testing.T) {
	a, p, id := storedFixture(t)
	a.native = &sidebarNative{nativePlatform: p, t: t}
	detail, err := a.Session(t.Context(), id, 30, 0)
	if err != nil || detail.Session.ID != id || detail.Session.Title != "stored" || detail.Session.TotalCost != 0.5 || len(detail.Messages) != 2 {
		t.Fatalf("conversation changed: %+v %v", detail, err)
	}
}

func TestSidebarQueryBudgetDoesNotGrowWithHistory(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	a, _, _ := storedFixture(t)
	source, err := a.SidebarSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.sidebarReader.Close() })
	for i := range 50 {
		publish(t, a, db.Session{ID: fmt.Sprintf("other-%d", i), Directory: "/other", Title: "other"})
	}
	second, err := New(t.Context(), &nativePlatform{id: "second", source: "second-source"}, a.store, a.history, a.owner)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, second, db.Session{ID: "second-native", Title: "second", Directory: "/second"})
	secondSource, err := second.SidebarSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.sidebarReader.Close() })
	// A summary reader would fail immediately. Keep the actual sidebar storage
	// and call chain, but remove the accounting tables it must not depend on.
	conn, err := sql.Open("sqlite", a.history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`DROP TABLE analytics_fact; DROP TABLE analytics_hour`); err != nil {
		t.Fatal(err)
	}
	exporter.Reset()
	rows, err := a.SidebarSessionsFor(t.Context(), []platforms.SidebarSource{source, secondSource}, platforms.SidebarQuery{Limit: 10})
	if err != nil || len(rows) != 10 {
		t.Fatalf("sidebar page: %d %v", len(rows), err)
	}
	queries := 0
	for _, span := range exporter.GetSpans() {
		if span.Name == "sql.conn.query" {
			queries++
		}
	}
	if queries != 1 {
		t.Fatalf("sidebar used %d SQL queries; want exactly one", queries)
	}
}

func TestSidebarFiltersTagsAndReadsAuthoritativeFlags(t *testing.T) {
	a, _, id := storedFixture(t)
	source, err := a.SidebarSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.sidebarReader.Close() })
	path, err := a.store.FilePath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	core, err := state.Open(path, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	identity, err := a.store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	catalogueSource := sessioncatalog.Source{OwnerID: identity.OwnerID, Platform: identity.Platform, SourceKey: identity.SourceKey}
	if _, err := a.store.Reconcile(t.Context(), catalogueSource, []sessioncatalog.NativeSession{{ID: identity.NativeID, Title: identity.Title, Directory: identity.Directory, Origin: sessioncatalog.Routine, OriginID: "routine-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := core.PinSession(t.Context(), "test", id); err != nil {
		t.Fatal(err)
	}
	if err := core.ArchiveSession(t.Context(), "test", id, 30); err != nil {
		t.Fatal(err)
	}
	if err := core.MarkSessionSeen(t.Context(), "test", id, 30); err != nil {
		t.Fatal(err)
	}
	if err := a.history.SetSidebarBranch(t.Context(), a.owner, "/stored", "fix/sidebar", 100); err != nil {
		t.Fatal(err)
	}
	rows, err := a.SidebarSessionsFor(t.Context(), []platforms.SidebarSource{source}, platforms.SidebarQuery{Since: 100, Origin: "routine", OriginID: "routine-1"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("pinned routine missing: %+v %v", rows, err)
	}
	row := rows[0]
	if !row.Pinned || !row.Archived || !row.Seen || row.RoutineID != "routine-1" || row.Branch != "fix/sidebar" || row.LastMessageAt != 20 {
		t.Fatalf("flags/metadata lost: %+v", row)
	}
	remoteRows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{UseSeenCutoffs: true, SeenCutoffs: map[string]int64{id: 10}})
	if err != nil || len(remoteRows) != 1 || remoteRows[0].UnreadCount != 1 || remoteRows[0].SeenTimeUpdated != 10 {
		t.Fatalf("viewer unread range ignored: %+v %v", remoteRows, err)
	}
	for _, q := range []platforms.SidebarQuery{{Origin: "factory"}, {OriginID: "other"}, {Directory: "/other"}, {Offset: 1}} {
		if rows, err := a.SidebarSessions(t.Context(), q); err != nil || len(rows) != 0 {
			t.Fatalf("filter ignored: %+v %v", rows, err)
		}
	}
	if err := a.store.Scrub(t.Context(), catalogueSource, identity.NativeID); err != nil {
		t.Fatal(err)
	}
	if rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{}); err != nil || len(rows) != 0 {
		t.Fatalf("purged identity leaked: %+v %v", rows, err)
	}
}

func TestSidebarBusyExpiresAndOldGenerationsCannotReviveIt(t *testing.T) {
	a, p, id := storedFixture(t)
	native := &sidebarNative{nativePlatform: p, t: t}
	a.native = native
	worker := &sidebarWorker{adapter: a}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer worker.Close(t.Context())
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	read := func() db.Session {
		t.Helper()
		rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("sidebar: %+v %v", rows, err)
		}
		return rows[0]
	}
	if row := read(); row.ID != id || row.Status != db.StatusBusy {
		t.Fatalf("live busy missing: %+v", row)
	}
	native.observations = map[string]platforms.LiveState{"native": {Status: "limited", ResetsAt: 1234}}
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := read(); row.Status != db.StatusLimited || row.ResetsAt != 1234 {
		t.Fatalf("rate-limit observation lost: %+v", row)
	}
	if err := a.history.ReplaceSidebarObservations(t.Context(), a.observationScope(), a.sidebarGeneration, time.Now().Add(-time.Second).UnixMilli(), map[string]statev2.SidebarObservation{"native": {Status: "busy"}}); err != nil {
		t.Fatal(err)
	}
	if row := read(); row.Status == db.StatusBusy || row.LiveConnection {
		t.Fatalf("expired busy survived: %+v", row)
	}
	native.observations = map[string]platforms.LiveState{}
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := read(); row.Status == db.StatusBusy {
		t.Fatalf("disconnect kept busy: %+v", row)
	}
}

func TestSidebarBubblesNativePromptsButNotApplicationLinks(t *testing.T) {
	a, p, id := storedFixture(t)
	publish(t, a, db.Session{ID: "child", ParentID: "native", Directory: "/stored", Title: "child"})
	native := &sidebarNative{nativePlatform: p, t: t, observations: map[string]platforms.LiveState{"native": {Status: "busy"}, "child": {Status: "busy", PendingPermission: true}}}
	a.native = native
	worker := &sidebarWorker{adapter: a}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer worker.Close(t.Context())
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{Since: 100})
	if err != nil || len(rows) != 1 || rows[0].ID != id || !rows[0].PendingPermission || rows[0].Status != db.StatusWaiting {
		t.Fatalf("native prompt not bubbled: %+v %v", rows, err)
	}
	detail, err := a.Session(t.Context(), id, 30, 0)
	if err != nil || !detail.Session.PendingPermission || detail.Session.Status != db.StatusWaiting {
		t.Fatalf("conversation lost native child prompt: %+v %v", detail, err)
	}
	linked, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: a.owner, Platform: a.source.Platform,
		Directory: "/stored", ParentID: id, Origin: sessioncatalog.User, RequestKey: "application-child", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Bind(t.Context(), linked.ID, a.binding("linked")); err != nil {
		t.Fatal(err)
	}
	publish(t, a, db.Session{ID: "linked", Directory: "/stored", Title: "application child"})
	native.observations = map[string]platforms.LiveState{"native": {Status: "busy"}, "linked": {Status: "busy", PendingPermission: true}}
	detail, err = a.Session(t.Context(), id, 30, 0)
	if err != nil || detail.Session.PendingPermission || detail.Session.Status != db.StatusBusy {
		t.Fatalf("conversation borrowed application child prompt: %+v %v", detail, err)
	}
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err = a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id && (row.PendingPermission || row.Status != db.StatusBusy) {
			t.Fatalf("application link borrowed prompt authority: %+v", row)
		}
	}
}
